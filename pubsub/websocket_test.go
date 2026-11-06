// Copyright (c) 2018-2021, The Decred developers
// Copyright (c) 2017, The dcrdata developers
// See LICENSE for details.

package pubsub

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	exptypes "github.com/decred/dcrdata/v8/explorer/types"
	pstypes "github.com/decred/dcrdata/v8/pubsub/types"
	"golang.org/x/net/websocket"
)

func Test_client_subscribe(t *testing.T) {
	tests := []struct {
		name    string
		cl      *client
		hubMsg  pstypes.HubMessage
		wantErr error
		wantOK  bool
	}{
		{"ping not a sub", newClient(), pstypes.HubMessage{Signal: sigPingAndUserCount}, nil, false},
		{"ok newtx", newClient(), pstypes.HubMessage{Signal: sigNewTx}, nil, true},
		{"ok addr", newClient(), pstypes.HubMessage{
			Signal: sigAddressTx,
			Msg:    &pstypes.AddressMessage{Address: "DsfX4WrSecUwGoRd9B7Lz1JjYssYaVKnjGC"},
		}, nil, true},
		{"bad addr", newClient(), pstypes.HubMessage{
			Signal: sigAddressTx,
			Msg:    pstypes.AddressMessage{Address: "DsfX4WrSecUwGoRd9B7Lz1JjYssYaVKnjGC"},
		}, errors.New("msg.Msg not a string (SigAddressTx): types.AddressMessage"), false},
		{"bad addr", newClient(), pstypes.HubMessage{
			Signal: sigAddressTx,
			Msg:    nil,
		}, errors.New("msg.Msg not a string (SigAddressTx): <nil>"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := tt.cl.subscribe(tt.hubMsg)
			if (err != nil) != (tt.wantErr != nil) ||
				(err != nil && err.Error() != tt.wantErr.Error()) {
				t.Errorf(`subscribe() error = "%v", wantErr "%v"`, err, tt.wantErr)
				return
			}
			if ok != tt.wantOK {
				t.Errorf("Did not subscribe to %v.", tt.hubMsg)
			}
		})
	}
}

func TestAddTxToBuffer(t *testing.T) {
	wsh := NewWebsocketHub()
	newTxsMsg := pstypes.HubMessage{Signal: sigNewTxs}

	// One subscribed client nearly ready to send, one subscribed client with
	// an empty buffer, and one client that is not subscribed to new txs.
	nearlyFull, empty, notSubd := newClient(), newClient(), newClient()
	for _, cl := range []*client{nearlyFull, empty} {
		if _, err := cl.subscribe(newTxsMsg); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < NewTxBufferSize-1; i++ {
		nearlyFull.newTxs.addTxToBuffer(&exptypes.MempoolTx{})
	}
	for _, cl := range []*client{nearlyFull, empty, notSubd} {
		spoke := make(hubSpoke)
		wsh.clients[&spoke] = cl
	}

	// The result must not depend on map iteration order.
	if !wsh.addTxToBuffer(&exptypes.MempoolTx{}) {
		t.Errorf("addTxToBuffer did not report a full buffer")
	}
	if n := len(nearlyFull.newTxs.t); n != NewTxBufferSize {
		t.Errorf("subscribed client buffer length %d, want %d", n, NewTxBufferSize)
	}
	if n := len(empty.newTxs.t); n != 1 {
		t.Errorf("subscribed client buffer length %d, want 1", n)
	}
	if n := len(notSubd.newTxs.t); n != 0 {
		t.Errorf("unsubscribed client buffer length %d, want 0", n)
	}

	// Unsubscribing discards the buffered transactions.
	if err := nearlyFull.unsubscribe(newTxsMsg); err != nil {
		t.Fatal(err)
	}
	if n := len(nearlyFull.newTxs.t); n != 0 {
		t.Errorf("buffer length after unsubscribe %d, want 0", n)
	}
}

func TestClientDisconnectUnregisters(t *testing.T) {
	psh := &PubSubHub{wsHub: NewWebsocketHub()}
	go psh.wsHub.Run()
	defer psh.wsHub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(psh.WebSocketHandler))
	defer srv.Close()

	waitNumClients := func(want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for psh.wsHub.NumClients() != want {
			if time.Now().After(deadline) {
				t.Fatalf("NumClients = %d, want %d", psh.wsHub.NumClients(), want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, err := websocket.Dial(url, "", srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	waitNumClients(1)

	// Closing the connection must unregister the client without waiting for
	// the next hub signal (the ping interval is much longer than the wait).
	ws.Close()
	waitNumClients(0)
}
