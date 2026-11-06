// Copyright (c) 2018-2021, The Decred developers
// Copyright (c) 2017, Jonathan Chappelow
// See LICENSE for details.

// Package notification synchronizes dcrd notifications to any number of
// handlers. Typical use:
//  1. Create a Notifier with NewNotifier.
//  2. Grab dcrd configuration settings with DcrdHandlers.
//  3. Create an dcrd/rpcclient.Client with the settings from step 2.
//  4. Add handlers with the Register*Group methods. You can add more than
//     one handler (a "group") at a time. Groups are run sequentially in the
//     order that they are registered, but the handlers within a group are run
//     asynchronously.
//  5. Set the previous best known block with SetPreviousBlock. By this point,
//     it should be certain that all of the data consumers are synced to the best
//     block.
//  6. **After all handlers have been added**, start the Notifier with Listen,
//     providing as an argument the dcrd client created in step 3.
package notification

import (
	"context"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/decred/dcrd/chaincfg/chainhash"
	chainjson "github.com/decred/dcrd/rpc/jsonrpc/types/v4"
	"github.com/decred/dcrd/rpcclient/v8"
	"github.com/decred/dcrd/wire"

	"github.com/decred/dcrdata/v8/rpcutils"
	"github.com/decred/dcrdata/v8/txhelpers"
)

// SyncHandlerDeadline is a hard deadline for handlers to finish handling. A
// block or reorg handler group that misses it is treated as a failure: the
// notification is abandoned and the fatal handler is invoked.
const SyncHandlerDeadline = time.Minute * 5

// maxQueuedTxns bounds the number of mempool transaction notifications waiting
// to be processed. Transactions beyond it are dropped rather than queued; the
// mempool monitor recollects the full mempool on every new block, so a dropped
// notification only delays when that transaction appears.
const maxQueuedTxns = 1024

// BranchTips describes the old and new chain tips involved in a reorganization.
type BranchTips struct {
	OldChainHead   chainhash.Hash
	OldChainHeight int32
	NewChainHead   chainhash.Hash
	NewChainHeight int32
}

// TxHandler is a function that will be called when dcrd reports new mempool
// transactions.
type TxHandler func(*chainjson.TxRawResult) error

// BlockHandler is a function that will be called when dcrd reports a new block.
type BlockHandler func(*wire.BlockHeader) error

// BlockHandlerLite is a simpler trigger using only bultin types, also called
// when dcrd reports a new block.
type BlockHandlerLite func(uint32, string) error

// ReorgHandler is a function that will be called when dcrd reports a reorg.
type ReorgHandler func(*txhelpers.ReorgData) error

// Notifier handles block, tx, and reorg notifications from a dcrd node. Handler
// functions are registered with the Register*Handlers methods. To start the
// Notifier, Listen must be called with a dcrd rpcclient.Client only after all
// handlers are registered.
type Notifier struct {
	node DCRDNode
	// The anyQ sequences all dcrd notification in the order they are received.
	anyQ     *ntfnQueue
	tx       [][]TxHandler
	block    [][]BlockHandler
	reorg    [][]ReorgHandler
	fatal    func()
	deadline time.Duration
	previous struct {
		hash   chainhash.Hash
		height uint32
	}
}

// NewNotifier is the constructor for a Notifier.
func NewNotifier() *Notifier {
	return &Notifier{
		anyQ:     newNtfnQueue(),
		tx:       make([][]TxHandler, 0),
		block:    make([][]BlockHandler, 0),
		reorg:    make([][]ReorgHandler, 0),
		deadline: SyncHandlerDeadline,
	}
}

// ntfnQueue is the FIFO between the dcrd notification callbacks and
// superQueue. The callbacks run on the rpcclient goroutine that reads the
// node's websocket, which is also the goroutine that delivers every RPC
// response. A callback that blocks therefore stalls every dcrd RPC, including
// the ones the handlers draining the queue are waiting on, which is a deadlock.
// So push never blocks: blocks and reorgs are always queued, since skipping one
// would leave the Notifier unable to connect any further blocks, and they
// arrive at the rate the chain produces them. Mempool transactions, whose rate
// is set by the network, are capped at maxQueuedTxns and dropped beyond that.
type ntfnQueue struct {
	mtx     sync.Mutex
	items   []interface{}
	numTxns int
	dropped int
	// ready holds a token whenever items may be non-empty.
	ready chan struct{}
}

func newNtfnQueue() *ntfnQueue {
	return &ntfnQueue{
		ready: make(chan struct{}, 1),
	}
}

// push appends a notification to the queue without blocking. It reports false
// if the notification was a transaction that was dropped because the queue
// already holds maxQueuedTxns transactions.
func (q *ntfnQueue) push(item interface{}) bool {
	_, isTx := item.(*chainjson.TxRawResult)

	q.mtx.Lock()
	if isTx && q.numTxns >= maxQueuedTxns {
		q.dropped++
		first := q.dropped == 1
		q.mtx.Unlock()
		if first {
			log.Warnf("Notification queue holds %d transactions. Dropping new "+
				"mempool transaction notifications until it drains.", maxQueuedTxns)
		}
		return false
	}
	if isTx {
		q.numTxns++
	}
	dropped := q.dropped
	q.dropped = 0
	q.items = append(q.items, item)
	q.mtx.Unlock()

	if dropped > 0 {
		log.Warnf("Dropped %d mempool transaction notifications while the "+
			"notification queue was full.", dropped)
	}

	select {
	case q.ready <- struct{}{}:
	default: // a token is already pending
	}
	return true
}

// pop removes and returns the oldest notification, waiting for one if the
// queue is empty. It returns false once ctx is done.
func (q *ntfnQueue) pop(ctx context.Context) (interface{}, bool) {
	for {
		if ctx.Err() != nil {
			return nil, false
		}
		q.mtx.Lock()
		if len(q.items) > 0 {
			item := q.items[0]
			q.items[0] = nil
			q.items = q.items[1:]
			if len(q.items) == 0 {
				q.items = nil // release the backing array
			}
			if _, isTx := item.(*chainjson.TxRawResult); isTx {
				q.numTxns--
			}
			q.mtx.Unlock()
			return item, true
		}
		q.mtx.Unlock()

		select {
		case <-q.ready:
		case <-ctx.Done():
			return nil, false
		}
	}
}

// DCRDNode is an interface to wrap a dcrd rpcclient.Client. The interface
// allows testing with a dummy node.
type DCRDNode interface {
	rpcutils.BlockFetcher
	NotifyBlocks(context.Context) error
	NotifyNewTransactions(context.Context, bool) error
}

// Listen must be called once, but only after all handlers are registered.
func (notifier *Notifier) Listen(ctx context.Context, dcrdClient DCRDNode) *ContextualError {
	// Register for block connection and chain reorg notifications.
	notifier.node = dcrdClient

	var err error
	if err = dcrdClient.NotifyBlocks(ctx); err != nil {
		return newContextualError("block notification "+
			"registration failed", err)
	}

	// Register for tx accepted into mempool ntfns
	if err = dcrdClient.NotifyNewTransactions(ctx, true); err != nil {
		return newContextualError("new transaction verbose notification registration failed", err)
	}

	go notifier.superQueue(ctx)
	return nil
}

// DcrdHandlers creates a set of handlers to be passed to the dcrd
// rpcclient.Client as a parameter of its constructor.
func (notifier *Notifier) DcrdHandlers() *rpcclient.NotificationHandlers {
	return &rpcclient.NotificationHandlers{
		OnBlockConnected:    notifier.onBlockConnected,
		OnBlockDisconnected: notifier.onBlockDisconnected,
		OnReorganization:    notifier.onReorganization,
		// OnTxAcceptedVerbose is invoked same as OnTxAccepted but is used here
		// for the mempool monitors to avoid an extra call to dcrd for
		// the tx details
		OnTxAcceptedVerbose: notifier.onTxAcceptedVerbose,
	}
}

// superQueue should be run as a goroutine. The dcrd-registered block and reorg
// handlers should perform any pre-processing and type conversion and then
// deposit the payload into the anyQ channel.
func (notifier *Notifier) superQueue(ctx context.Context) {
	for {
		rawMsg, ok := notifier.anyQ.pop(ctx)
		if !ok {
			return
		}
		// Do not allow new blocks to process while running reorg. Only allow
		// them to be processed after this reorg completes.
		switch msg := rawMsg.(type) {
		case *wire.BlockHeader:
			// Process the new block.
			log.Infof("superQueue: Processing new block %v (height %d).", msg.BlockHash(), msg.Height)
			notifier.processBlock(msg)
		case BranchTips:
			// Process the reorg.
			log.Infof("superQueue: Processing reorganization from %s (height %d) to %s (height %d).",
				msg.OldChainHead.String(), msg.OldChainHeight,
				msg.NewChainHead.String(), msg.NewChainHeight)
			notifier.signalReorg(msg)
		case *chainjson.TxRawResult:
			notifier.processTx(msg)
		default:
			log.Warnf("unknown message type in superQueue: %T", rawMsg)
		}
	}
}

// rpcclient.NotificationHandlers.OnBlockConnected
// TODO: considering using txns [][]byte to save on downstream RPCs.
func (notifier *Notifier) onBlockConnected(blockHeaderSerialized []byte, _ [][]byte) {
	blockHeader := new(wire.BlockHeader)
	err := blockHeader.FromBytes(blockHeaderSerialized)
	if err != nil {
		log.Error("Failed to deserialize blockHeader in new block notification: "+
			"%v", err)
		return
	}
	height := int32(blockHeader.Height)
	hash := blockHeader.BlockHash()
	prevHash := blockHeader.PrevBlock // to ensure this is the next block

	log.Debugf("OnBlockConnected: %d / %v (previous: %v)", height, hash, prevHash)

	notifier.anyQ.push(blockHeader)
}

// rpcclient.NotificationHandlers.OnBlockDisconnected
func (notifier *Notifier) onBlockDisconnected(blockHeaderSerialized []byte) {
	blockHeader := new(wire.BlockHeader)
	err := blockHeader.FromBytes(blockHeaderSerialized)
	if err != nil {
		log.Error("Failed to deserialize blockHeader in block disconnect notification: "+
			"%v", err)
		return
	}
	height := int32(blockHeader.Height)
	hash := blockHeader.BlockHash()

	log.Debugf("OnBlockDisconnected: %d / %v", height, hash)
	// this just logs
}

// rpcclient.NotificationHandlers.OnReorganization
// Processes the arguments into a BranchTips before submitting to the anyQ.
func (notifier *Notifier) onReorganization(oldHash *chainhash.Hash, oldHeight int32,
	newHash *chainhash.Hash, newHeight int32) {
	log.Tracef("OnReorganization: %d / %s --> %d / %s",
		oldHeight, oldHash, newHeight, newHash)

	notifier.anyQ.push(BranchTips{
		OldChainHead:   *oldHash,
		OldChainHeight: oldHeight,
		NewChainHead:   *newHash,
		NewChainHeight: newHeight,
	})
}

// rpcclient.NotificationHandlers.OnTxAcceptedVerbose
func (notifier *Notifier) onTxAcceptedVerbose(tx *chainjson.TxRawResult) {
	// Current UNIX time to assign the new transaction.
	tx.Time = time.Now().Unix()
	notifier.anyQ.push(tx)
}

// RegisterTxHandlerGroup adds a group of tx handlers. Groups are run
// sequentially in the order they are registered, but the handlers within the
// group are run asynchronously.
func (notifier *Notifier) RegisterTxHandlerGroup(handlers ...TxHandler) {
	notifier.tx = append(notifier.tx, handlers)
}

// RegisterBlockHandlerGroup adds a group of block handlers. Groups are run
// sequentially in the order they are registered, but the handlers within the
// group are run asynchronously. Handlers registered with
// RegisterBlockHandlerGroup are FIFO'd together with handlers registered with
// RegisterBlockHandlerLiteGroup.
func (notifier *Notifier) RegisterBlockHandlerGroup(handlers ...BlockHandler) {
	notifier.block = append(notifier.block, handlers)
}

// RegisterBlockHandlerLiteGroup adds a group of block handlers. Groups are run
// sequentially in the order they are registered, but the handlers within the
// group are run asynchronously. This method differs from
// RegisterBlockHandlerGroup in that the handlers take no arguments, so their
// packages don't necessarily need to import dcrd/wire. Handlers registered with
// RegisterBlockHandlerLiteGroup are FIFO'd together with handlers registered
// with RegisterBlockHandlerGroup.
func (notifier *Notifier) RegisterBlockHandlerLiteGroup(handlers ...BlockHandlerLite) {
	translations := make([]BlockHandler, 0, len(handlers))
	for i := range handlers {
		handler := handlers[i]
		translations = append(translations, func(block *wire.BlockHeader) error {
			return handler(block.Height, block.BlockHash().String())
		})
	}
	notifier.RegisterBlockHandlerGroup(translations...)
}

// RegisterReorgHandlerGroup adds a group of reorg handlers. Groups are run
// sequentially in the order they are registered, but the handlers within the
// group are run asynchronously.
func (notifier *Notifier) RegisterReorgHandlerGroup(handlers ...ReorgHandler) {
	notifier.reorg = append(notifier.reorg, handlers)
}

// SetFatalHandler registers a callback invoked when a block or reorg handler
// group fails or misses SyncHandlerDeadline, or a reorg cannot be resolved, and
// the notification is abandoned. Once that happens the Notifier cannot connect
// any further blocks — the abandoned block is deliberately not recorded, so
// every subsequent block fails the connects-to check in processBlock — so the
// intended callback is requestShutdown. Restarting resumes from the last
// consistent height via the batch sync, which is the only thing that can get
// the data stores moving again. With no handler set the Notifier simply stops
// connecting blocks.
func (notifier *Notifier) SetFatalHandler(f func()) {
	notifier.fatal = f
}

// SetPreviousBlock modifies the height and hash of the best block. This data is
// required to avoid connecting new blocks that are not next in the chain. It is
// only necessary to call SetPreviousBlock if blocks are connected or
// disconnected by a mechanism other than (*Notifier).processBlock, which
// keeps this data up-to-date. For example, signalReorg will use
// SetPreviousBlock after the reorg is complete.
func (notifier *Notifier) SetPreviousBlock(prevHash chainhash.Hash, prevHeight uint32) {
	notifier.previous.hash = prevHash
	notifier.previous.height = prevHeight
}

// abandon invokes the fatal handler, if one is set, after a block or reorg
// notification has been abandoned.
func (notifier *Notifier) abandon() {
	if notifier.fatal != nil {
		notifier.fatal()
	}
}

func functionName(i interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(i).Pointer()).Name()
}

// processBlock calls the BlockHandler/BlockHandlerLite groups one at a time in
// the order that they were registered.
func (notifier *Notifier) processBlock(bh *wire.BlockHeader) {
	hash := bh.BlockHash()
	height := bh.Height
	prev := notifier.previous

	// Ensure that the received block (bh.hash, bh.height) connects to the
	// previously connected block (q.prevHash, q.prevHeight).
	if bh.PrevBlock != prev.hash {
		log.Infof("Received block at %d (%v) does not connect to %d (%v). "+
			"This is normal before reorganization.",
			height, hash, prev.height, prev.hash)
		return
	}

	start := time.Now()
	for _, handlers := range notifier.block {
		wg := new(sync.WaitGroup)
		var failed atomic.Bool
		for _, h := range handlers {
			wg.Add(1)
			go func(h BlockHandler) {
				tStart := time.Now()
				defer wg.Done()
				defer func() {
					log.Tracef("Notifier: BlockHandler %s completed in %v",
						functionName(h), time.Since(tStart))
				}()
				if err := h(bh); err != nil {
					log.Errorf("block handler %s failed for block %d (%v): %v",
						functionName(h), height, hash, err)
					failed.Store(true)
					return
				}
			}(h)
		}
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(notifier.deadline):
			// A handler still running past the deadline is no different from
			// one that failed: the block cannot be recorded as connected, so
			// every later block would fail the connects-to check above.
			log.Errorf("Abandoning block %d (%v): at least 1 block handler has "+
				"not completed before the deadline. No further blocks can be "+
				"connected; restarting resumes from the last consistent height.",
				height, hash)
			notifier.abandon()
			return
		}

		// Handler groups are registered in dependency order: the stake database
		// is connected before the block is stored in PostgreSQL. Running the
		// later groups anyway after an earlier one failed is what leaves the two
		// stores at different heights — a state dcrdata cannot repair on its own
		// (the only rewind fires when the stake database is ahead) and which
		// panics the next batch sync. Abandon the block instead, leaving
		// notifier.previous untouched so nothing is recorded as connected.
		if failed.Load() {
			log.Errorf("Abandoning block %d (%v) after a handler failure. No "+
				"further blocks can be connected; restarting resumes from the "+
				"last consistent height.", height, hash)
			notifier.abandon()
			return
		}
	}
	log.Debugf("handlers of Notifier.processBlock() completed in %v", time.Since(start))

	// Record this block as the best block connected by the collectionQueue.
	notifier.SetPreviousBlock(hash, height)
}

// processTx calls the TxHandler groups one at a time in the order that they
// were registered.
func (notifier *Notifier) processTx(tx *chainjson.TxRawResult) {
	start := time.Now()
	for i, handlers := range notifier.tx {
		wg := new(sync.WaitGroup)
		for j, h := range handlers {
			wg.Add(1)
			go func(h TxHandler, i, j int) {
				defer wg.Done()
				defer log.Tracef("Notifier: TxHandler %d.%d completed", i, j)
				if err := h(tx); err != nil {
					log.Errorf("tx handler failed: %v", err)
					return
				}
			}(h, i, j)
		}
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(notifier.deadline):
			log.Errorf("at least 1 tx handler has not completed before the deadline")
			return
		}
	}
	log.Tracef("handlers of Notifier.onTxAcceptedVerbose() completed in %v", time.Since(start))
}

// signalReorg takes the basic reorganization data from dcrd, determines the two
// chains and their common ancestor, and signals the reorg to each ReorgHandler
// group, one at a time in the order that they were registered. Lastly, the
// Notifier's best block data is updated so that it will successfully accept new
// blocks building on the new chain tip.
func (notifier *Notifier) signalReorg(d BranchTips) {
	// Determine the common ancestor of the two chains, and get the full
	// list of blocks in each chain back to but not including the common
	// ancestor.
	ancestor, newChain, oldChain, err := rpcutils.CommonAncestor(notifier.node,
		d.NewChainHead, d.OldChainHead)
	if err != nil {
		// Nothing has changed in the data stores, but the new chain's blocks
		// do not connect to the recorded best block, so none could be
		// connected after this.
		log.Errorf("Failed to determine common ancestor: %v. Aborting reorg to "+
			"%v (%d). No further blocks can be connected; restarting resumes "+
			"from the last consistent height.", err, d.NewChainHead, d.NewChainHeight)
		notifier.abandon()
		return
	}

	reorg := &txhelpers.ReorgData{
		CommonAncestor: *ancestor,
		NewChain:       newChain,
		NewChainHead:   d.NewChainHead,
		NewChainHeight: d.NewChainHeight,
		OldChain:       oldChain,
		OldChainHead:   d.OldChainHead,
		OldChainHeight: d.OldChainHeight,
	}

	start := time.Now()
	for i, handlers := range notifier.reorg {
		wg := new(sync.WaitGroup)
		var failed atomic.Bool
		for j, h := range handlers {
			wg.Add(1)
			go func(h ReorgHandler, i, j int) {
				defer wg.Done()
				defer log.Debugf("Notifier: ReorgHandler %d.%d completed", i, j)
				if err := h(reorg); err != nil {
					log.Errorf("reorg handler %d.%d failed: %v", i, j, err)
					failed.Store(true)
					return
				}
			}(h, i, j)
		}
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(notifier.deadline):
			log.Errorf("Abandoning reorg to %v (%d): at least 1 reorg handler "+
				"has not completed before the deadline. No further blocks can be "+
				"connected; restarting resumes from the last consistent height.",
				d.NewChainHead, d.NewChainHeight)
			notifier.abandon()
			return
		}

		// As in processBlock: the reorg groups are ordered stake database first,
		// then blockdata and chainDB. Carrying on past a failure would move one
		// store to the new chain and leave the other behind, so abandon the
		// reorg rather than record the new chain head below.
		if failed.Load() {
			log.Errorf("Abandoning reorg to %v (%d) after a handler failure. No "+
				"further blocks can be connected; restarting resumes from the "+
				"last consistent height.", d.NewChainHead, d.NewChainHeight)
			notifier.abandon()
			return
		}
	}
	log.Debugf("handlers of Notifier.signalReorg() completed in %v", time.Since(start))

	// Update prevHash and prevHeight in collectionQueue.
	notifier.SetPreviousBlock(d.NewChainHead, uint32(d.NewChainHeight))
}
