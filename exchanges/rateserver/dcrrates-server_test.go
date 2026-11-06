// Copyright (c) 2019-2021, The Decred developers
// See LICENSE for details.

package main

import (
	"context"
	"flag"
	"os"
	"testing"
	"time"

	"github.com/decred/dcrdata/exchanges/v3"
	dcrrates "github.com/decred/dcrdata/exchanges/v3/ratesproto"
)

func TestAddDeleteClient(t *testing.T) {
	server := NewRateServer("", nil)
	_, sid := server.addClient(nil, nil)
	if len(server.clients) != 1 {
		t.Fatalf("client length after addClient: %d, expecting 1", len(server.clients))
	}
	server.deleteClient(sid)
	if len(server.clients) != 0 {
		t.Fatalf("client length after deleteClient %d, expecting 0", len(server.clients))
	}
}

type clientStub struct {
	dcrExchanges map[string]map[exchanges.CurrencyPair]*exchanges.ExchangeState
}

func (c *clientStub) SendExchangeUpdate(update *dcrrates.ExchangeRateUpdate) error {
	if c.dcrExchanges == nil {
		c.dcrExchanges = make(map[string]map[exchanges.CurrencyPair]*exchanges.ExchangeState)
	}

	if c.dcrExchanges[update.Token] == nil {
		c.dcrExchanges[update.Token] = make(map[exchanges.CurrencyPair]*exchanges.ExchangeState)
	}

	currencyPair := exchanges.CurrencyPair(update.CurrencyPair)
	c.dcrExchanges[update.Token][currencyPair] = &exchanges.ExchangeState{
		BaseState: exchanges.BaseState{
			Price:      update.GetPrice(),
			BaseVolume: update.GetBaseVolume(),
			Volume:     update.GetVolume(),
			Change:     update.GetChange(),
			Stamp:      update.GetStamp(),
		},
	}

	return nil
}

func (c *clientStub) Stream() GRPCStream {
	return nil
}

// streamStub is a GRPCStream whose Send blocks until unblock is closed.
type streamStub struct {
	ctx     context.Context
	sent    chan *dcrrates.ExchangeRateUpdate
	unblock chan struct{}
}

func (s *streamStub) Send(update *dcrrates.ExchangeRateUpdate) error {
	<-s.unblock
	s.sent <- update
	return nil
}

func (s *streamStub) Context() context.Context {
	return s.ctx
}

func newTestServer(t *testing.T) *RateServer {
	t.Helper()
	xcBot, err := exchanges.NewExchangeBot(&exchanges.ExchangeBotConfig{Index: "USD"})
	if err != nil {
		t.Fatalf("NewExchangeBot error: %v", err)
	}
	return NewRateServer("USD", xcBot)
}

func subscribe(server *RateServer, stream GRPCStream) chan error {
	errC := make(chan error, 1)
	go func() {
		errC <- server.ReallySubscribeExchanges(&dcrrates.ExchangeSubscription{
			Index:     "USD",
			Exchanges: []string{"tokenA"},
		}, stream)
	}()
	return errC
}

func waitForClients(t *testing.T, server *RateServer, n int) {
	t.Helper()
	for tStart := time.Now(); time.Since(tStart) < 5*time.Second; time.Sleep(time.Millisecond) {
		server.clientLock.RLock()
		count := len(server.clients)
		server.clientLock.RUnlock()
		if count == n {
			return
		}
	}
	t.Fatalf("timed out waiting for %d clients", n)
}

// TestSubscriberDelivery checks that queued updates are delivered, and that the
// client is removed from the map when its stream ends.
func TestSubscriberDelivery(t *testing.T) {
	server := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	stream := &streamStub{
		ctx:     ctx,
		sent:    make(chan *dcrrates.ExchangeRateUpdate, 1),
		unblock: make(chan struct{}),
	}
	close(stream.unblock)
	errC := subscribe(server, stream)
	waitForClients(t, server, 1)

	server.sendUpdate(&dcrrates.ExchangeRateUpdate{Token: "tokenB"}) // not subscribed
	server.sendUpdate(&dcrrates.ExchangeRateUpdate{Token: "tokenA", Price: 1})
	select {
	case update := <-stream.sent:
		if update.Token != "tokenA" || update.Price != 1 {
			t.Fatalf("wrong update received: %v", update)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("update not delivered")
	}

	cancel()
	if err := <-errC; err != nil {
		t.Fatalf("unexpected error after stream cancellation: %v", err)
	}
	waitForClients(t, server, 0)
}

// TestSlowSubscriber checks that a client that stops reading neither blocks
// sendUpdate nor remains in the client map.
func TestSlowSubscriber(t *testing.T) {
	server := newTestServer(t)
	stream := &streamStub{
		ctx:     context.Background(),
		sent:    make(chan *dcrrates.ExchangeRateUpdate, clientQueueSize+2),
		unblock: make(chan struct{}),
	}
	errC := subscribe(server, stream)
	waitForClients(t, server, 1)

	sent := make(chan struct{})
	go func() {
		for i := 0; i < clientQueueSize+2; i++ {
			server.sendUpdate(&dcrrates.ExchangeRateUpdate{Token: "tokenA"})
		}
		close(sent)
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatalf("sendUpdate blocked on a client that is not reading")
	}

	// Once the blocked Send returns, the client is disconnected and removed.
	close(stream.unblock)
	if err := <-errC; err == nil {
		t.Fatalf("expected an error for a client that fell behind")
	}
	waitForClients(t, server, 0)
}

func TestSendStateList(t *testing.T) {
	updates := make(map[string]map[exchanges.CurrencyPair]*exchanges.ExchangeState)
	currencyPair := exchanges.CurrencyPairDCRBTC
	xcToken := "DummyToken"
	updates[xcToken] = map[exchanges.CurrencyPair]*exchanges.ExchangeState{
		currencyPair: {},
	}

	client := &clientStub{}
	err := sendStateList(client, updates)
	if err != nil {
		t.Fatalf("Error sending exchange states: %v", err)
	}

	if client.dcrExchanges[xcToken][currencyPair] == nil {
		t.Fatalf("expected at least one exchange state for currency pair %s", currencyPair)
	}
}

type certWriterStub struct {
	lengths map[string]int
}

func (w certWriterStub) WriteCertificate(certPath string, cert []byte) error {
	w.lengths[certPath] = len(cert)
	return nil
}

// TestDefaultAltDNSNames ensures that there are no additional hostnames added
// by default during the configuration load phase.
func TestDefaultAltDNSNames(t *testing.T) {
	// Parse the -test.* flags before removing them from the command line
	// arguments list, which we do to allow go-flags to succeed.
	flag.Parse()
	os.Args = os.Args[:1]

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("Failed to load dcrd config: %s", err)
	}
	if len(cfg.AltDNSNames) != 0 {
		t.Fatalf("Invalid default value for altdnsnames: %s", cfg.AltDNSNames)
	}
}

func TestGenerateRPCKeyPair(t *testing.T) {
	writer := certWriterStub{lengths: make(map[string]int)}
	_, err := generateRPCKeyPair("./cert", "./key", []string(nil), writer)
	if err != nil {
		t.Fatalf("Error generating TLS certificate: %v", err)
	}
	certLen, ok := writer.lengths["./cert"]
	if !ok {
		t.Fatal("Dummy certificate path index not found")
	}
	if certLen == 0 {
		t.Fatal("Zero length certificate")
	}
	keyLen, ok := writer.lengths["./key"]
	if !ok {
		t.Fatal("Dummy key path index not found")
	}
	if keyLen == 0 {
		t.Fatal("Zero length key")
	}
}
