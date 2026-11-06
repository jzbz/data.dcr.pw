package explorer

import (
	"sync"
	"testing"
	"time"

	pstypes "github.com/decred/dcrdata/v8/pubsub/types"
)

// TestWebsocketHubSlowExchangeClient covers a client that stops reading
// exchange updates. The run loop serves every client, so a blocking send to
// that one client would stop all signals, registration and unregistration. The
// client must be dropped instead, and a client that keeps up must be
// unaffected.
func TestWebsocketHubSlowExchangeClient(t *testing.T) {
	wsh := NewWebsocketHub()
	go wsh.run()
	stop := sync.OnceFunc(wsh.Stop)
	defer stop()

	stalledSig := make(hubSpoke, 3)
	stalledXc := make(exchangeChannel, 3)
	wsh.RegisterClient(&stalledSig, stalledXc)

	healthySig := make(hubSpoke, 3)
	healthyXc := make(exchangeChannel, 3)
	wsh.RegisterClient(&healthySig, healthyXc)

	const timeout = 5 * time.Second
	for i := 0; i < 10; i++ {
		update := &WebsocketExchangeUpdate{Price: float64(i)}
		select {
		case wsh.xcChan <- update:
		case <-time.After(timeout):
			t.Fatalf("hub stopped accepting exchange updates after %d", i)
		}
		select {
		case got := <-healthyXc:
			if got != update {
				t.Fatalf("healthy client got update %v, want %v", got.Price, update.Price)
			}
		case <-time.After(timeout):
			t.Fatalf("healthy client did not receive exchange update %d", i)
		}
	}

	// The run loop must still relay signals.
	select {
	case wsh.HubRelay <- pstypes.HubMessage{Signal: sigPingAndUserCount}:
	case <-time.After(timeout):
		t.Fatal("hub run loop is blocked")
	}
	select {
	case msg := <-healthySig:
		if msg.Signal != sigPingAndUserCount {
			t.Errorf("healthy client got signal %v, want %v", msg.Signal, sigPingAndUserCount)
		}
	case <-time.After(timeout):
		t.Fatal("healthy client did not receive the ping")
	}

	// The stalled client was unregistered, which closes its signal channel.
	select {
	case _, ok := <-stalledSig:
		if ok {
			t.Error("stalled client received a signal instead of being unregistered")
		}
	case <-time.After(timeout):
		t.Fatal("stalled client was not unregistered")
	}
	if n := wsh.NumClients(); n != 1 {
		t.Errorf("NumClients = %d, want 1", n)
	}

	// Its handler's deferred UnregisterClient must not block, before or after
	// the hub stops.
	done := make(chan struct{})
	go func() {
		wsh.UnregisterClient(&stalledSig)
		stop()
		wsh.UnregisterClient(&healthySig)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("UnregisterClient blocked")
	}
}
