// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/hessed-os/mcp-go-sdk/auth"
	"github.com/hessed-os/mcp-go-sdk/jsonrpc"
	"golang.org/x/oauth2"
)

// eventsTransports are the ways the events tests reach a server: in memory
// with the legacy initialize handshake, in memory with server/discover, and
// over stateless streamable HTTP with server/discover.
var eventsTransports = []string{"memory/initialize", "memory/discover", "streamable-stateless"}

// connectEvents connects a client to server over the named transport.
func connectEvents(t *testing.T, server *Server, transport string) *ClientSession {
	t.Helper()
	ctx := context.Background()
	var (
		ct      Transport
		version = protocolVersion20260728
	)
	switch transport {
	case "memory/initialize", "memory/discover":
		if transport == "memory/initialize" {
			version = protocolVersion20251125
		}
		var st Transport
		ct, st = NewInMemoryTransports()
		ss, err := server.Connect(ctx, st, nil)
		if err != nil {
			t.Fatalf("server connect: %v", err)
		}
		t.Cleanup(func() { ss.Close() })
	case "streamable-stateless":
		handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{Stateless: true})
		httpServer := httptest.NewServer(mustNotPanic(t, handler))
		t.Cleanup(httpServer.Close)
		ct = &StreamableClientTransport{Endpoint: httpServer.URL}
	default:
		t.Fatalf("unknown transport %q", transport)
	}
	cs, err := NewClient(testImpl, nil).Connect(ctx, ct, &ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	if got := cs.InitializeResult().ProtocolVersion; got != version {
		t.Fatalf("negotiated protocol %q, want %q", got, version)
	}
	return cs
}

// eventsRecorder holds the requests its handlers received.
type eventsRecorder struct {
	list        chan *ServerRequest[*ListEventsParams]
	subscribe   chan *ServerRequest[*SubscribeEventParams]
	unsubscribe chan *ServerRequest[*UnsubscribeEventParams]
}

var testEventDefinition = &EventDefinition{
	Name:          "comment.created",
	Description:   "A new review comment was added to the specified document.",
	Delivery:      []string{"webhook"},
	InputSchema:   json.RawMessage(`{"type":"object","properties":{"document_id":{"type":"string"}},"required":["document_id"]}`),
	PayloadSchema: json.RawMessage(`{"type":"object","properties":{"comment_id":{"type":"string"}}}`),
}

var testRefreshBefore = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

// eventsOptions returns server options whose events handlers record their
// requests in rec and answer with fixed results.
func eventsOptions(rec *eventsRecorder) *ServerOptions {
	return &ServerOptions{
		ListEventsHandler: func(_ context.Context, req *ServerRequest[*ListEventsParams]) (*ListEventsResult, error) {
			rec.list <- req
			return &ListEventsResult{Events: []*EventDefinition{testEventDefinition}}, nil
		},
		SubscribeEventHandler: func(_ context.Context, req *ServerRequest[*SubscribeEventParams]) (*SubscribeEventResult, error) {
			rec.subscribe <- req
			refresh := testRefreshBefore
			return &SubscribeEventResult{ID: "sub_123", RefreshBefore: &refresh}, nil
		},
		UnsubscribeEventHandler: func(_ context.Context, req *ServerRequest[*UnsubscribeEventParams]) (*UnsubscribeEventResult, error) {
			rec.unsubscribe <- req
			return &UnsubscribeEventResult{}, nil
		},
	}
}

func newEventsRecorder() *eventsRecorder {
	return &eventsRecorder{
		list:        make(chan *ServerRequest[*ListEventsParams], 1),
		subscribe:   make(chan *ServerRequest[*SubscribeEventParams], 1),
		unsubscribe: make(chan *ServerRequest[*UnsubscribeEventParams], 1),
	}
}

func received[T any](t *testing.T, ch chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the handler")
		panic("unreachable")
	}
}

func TestEventsCapability(t *testing.T) {
	for _, transport := range eventsTransports {
		for _, withHandlers := range []bool{true, false} {
			name := transport + "/without-handlers"
			if withHandlers {
				name = transport + "/with-handlers"
			}
			t.Run(name, func(t *testing.T) {
				opts := &ServerOptions{}
				if withHandlers {
					opts = eventsOptions(newEventsRecorder())
				}
				cs := connectEvents(t, NewServer(testImpl, opts), transport)
				caps := cs.InitializeResult().Capabilities
				if got := caps.Events != nil; got != withHandlers {
					t.Errorf("capabilities.events present = %v, want %v", got, withHandlers)
				}
				data, err := json.Marshal(caps)
				if err != nil {
					t.Fatal(err)
				}
				if got := strings.Contains(string(data), `"events":{}`); got != withHandlers {
					t.Errorf("capabilities %s: contains \"events\":{} = %v, want %v", data, got, withHandlers)
				}
			})
		}
	}
}

func TestEventsCapabilityClone(t *testing.T) {
	// ServerCapabilities.clone keeps Events. (EventsCapabilities has no
	// fields, so a copy cannot be told from the original by address.)
	caps := (&ServerCapabilities{Events: &EventsCapabilities{}}).clone()
	if caps.Events == nil {
		t.Error("clone dropped capabilities.events")
	}
}

func TestEventsHandlersRequireEachOther(t *testing.T) {
	full := eventsOptions(newEventsRecorder())
	tests := []struct {
		name      string
		opts      ServerOptions
		wantPanic string
	}{
		{"list only", ServerOptions{ListEventsHandler: full.ListEventsHandler}, "ListEventsHandler requires SubscribeEventHandler"},
		{"subscribe only", ServerOptions{SubscribeEventHandler: full.SubscribeEventHandler}, "SubscribeEventHandler requires ListEventsHandler"},
		{"no unsubscribe", ServerOptions{ListEventsHandler: full.ListEventsHandler, SubscribeEventHandler: full.SubscribeEventHandler}, "ListEventsHandler requires UnsubscribeEventHandler"},
		{"unsubscribe only", ServerOptions{UnsubscribeEventHandler: full.UnsubscribeEventHandler}, "UnsubscribeEventHandler requires ListEventsHandler"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != test.wantPanic {
					t.Errorf("panic %v, want %q", got, test.wantPanic)
				}
			}()
			NewServer(testImpl, &test.opts)
		})
	}
}

func TestEventsRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, transport := range eventsTransports {
		t.Run(transport, func(t *testing.T) {
			rec := newEventsRecorder()
			cs := connectEvents(t, NewServer(testImpl, eventsOptions(rec)), transport)

			list, err := CallCustomMethod[*ListEventsParams, *ListEventsResult](ctx, cs, methodListEvents, &ListEventsParams{Cursor: "page-1"})
			if err != nil {
				t.Fatalf("events/list: %v", err)
			}
			if diff := cmp.Diff([]*EventDefinition{testEventDefinition}, list.Events); diff != "" {
				t.Errorf("events/list result mismatch (-want +got):\n%s", diff)
			}
			if got := received(t, rec.list).Params.Cursor; got != "page-1" {
				t.Errorf("events/list cursor %q, want page-1", got)
			}

			ttl := int64(3_600_000)
			for _, sub := range []*SubscribeEventParams{
				{Name: "comment.created"},
				{Name: "comment.created", NoExpiry: true},
				{Name: "comment.created", TTLMs: &ttl},
			} {
				sub.Arguments = json.RawMessage(`{"document_id":"doc_123"}`)
				sub.Delivery = &EventDeliveryTarget{Mode: "webhook", URL: "https://receiver.example.com/cb", Secret: "whsec_c2VjcmV0"}
				res, err := CallCustomMethod[*SubscribeEventParams, *SubscribeEventResult](ctx, cs, methodSubscribeEvent, sub)
				if err != nil {
					t.Fatalf("events/subscribe: %v", err)
				}
				want := &SubscribeEventResult{ID: "sub_123", RefreshBefore: &testRefreshBefore}
				if diff := cmp.Diff(want, res, cmp.FilterPath(func(p cmp.Path) bool { return p.Last().String() == ".Meta" }, cmp.Ignore())); diff != "" {
					t.Errorf("events/subscribe result mismatch (-want +got):\n%s", diff)
				}
				got := received(t, rec.subscribe).Params
				if diff := cmp.Diff(sub, got, cmp.FilterPath(func(p cmp.Path) bool { return p.Last().String() == ".Meta" }, cmp.Ignore())); diff != "" {
					t.Errorf("events/subscribe params mismatch (-sent +received):\n%s", diff)
				}
			}

			unsub := &UnsubscribeEventParams{
				Name:      "comment.created",
				Arguments: json.RawMessage(`{"document_id":"doc_123"}`),
				Delivery:  &EventDeliveryTarget{Mode: "webhook", URL: "https://receiver.example.com/cb"},
			}
			if _, err := CallCustomMethod[*UnsubscribeEventParams, *UnsubscribeEventResult](ctx, cs, methodUnsubscribeEvent, unsub); err != nil {
				t.Fatalf("events/unsubscribe: %v", err)
			}
			got := received(t, rec.unsubscribe).Params
			if diff := cmp.Diff(unsub, got, cmp.FilterPath(func(p cmp.Path) bool { return p.Last().String() == ".Meta" }, cmp.Ignore())); diff != "" {
				t.Errorf("events/unsubscribe params mismatch (-sent +received):\n%s", diff)
			}
		})
	}
}

// TestSubscribeEventParamsDecoding checks how the server decodes the wire
// form of events/subscribe params, in particular the three meanings of ttlMs.
func TestSubscribeEventParamsDecoding(t *testing.T) {
	const prefix = `{"name":"comment.created","arguments":{"document_id":"doc_123"},` +
		`"delivery":{"mode":"webhook","url":"https://receiver.example.com/cb","secret":"whsec_c2VjcmV0"},"cursor":`
	ttl := int64(60000)
	cursor := "c-7"
	tests := []struct {
		name string
		wire string
		want *SubscribeEventParams
	}{
		{"ttlMs absent", prefix + `null}`, &SubscribeEventParams{}},
		{"ttlMs null", prefix + `null,"ttlMs":null}`, &SubscribeEventParams{NoExpiry: true}},
		{"ttlMs number", prefix + `null,"ttlMs":60000}`, &SubscribeEventParams{TTLMs: &ttl}},
		{"cursor set", prefix + `"c-7"}`, &SubscribeEventParams{Cursor: &cursor}},
	}
	info := serverMethodInfos[methodSubscribeEvent]
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params, err := info.unmarshalParams(json.RawMessage(test.wire))
			if err != nil {
				t.Fatal(err)
			}
			p := params.(*SubscribeEventParams)
			want := test.want
			want.Name = "comment.created"
			want.Arguments = json.RawMessage(`{"document_id":"doc_123"}`)
			want.Delivery = &EventDeliveryTarget{Mode: "webhook", URL: "https://receiver.example.com/cb", Secret: "whsec_c2VjcmV0"}
			if diff := cmp.Diff(want, p); diff != "" {
				t.Errorf("decoded params mismatch (-want +got):\n%s", diff)
			}
			// Encoding gives back the same wire form.
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != test.wire {
				t.Errorf("re-encoded\n%s\nwant\n%s", data, test.wire)
			}
		})
	}

	if _, err := info.unmarshalParams(json.RawMessage(prefix + `null,"ttlMs":"soon"}`)); err == nil {
		t.Error("ttlMs \"soon\" decoded without error")
	}
}

func TestEventsHandlerErrorKeepsCode(t *testing.T) {
	ctx := context.Background()
	const codeCallbackEndpoint = -32015
	for _, transport := range eventsTransports {
		t.Run(transport, func(t *testing.T) {
			opts := eventsOptions(newEventsRecorder())
			opts.SubscribeEventHandler = func(context.Context, *ServerRequest[*SubscribeEventParams]) (*SubscribeEventResult, error) {
				return nil, &jsonrpc.Error{
					Code:    codeCallbackEndpoint,
					Message: "callback endpoint error",
					Data:    json.RawMessage(`{"reason":"challenge_failed"}`),
				}
			}
			cs := connectEvents(t, NewServer(testImpl, opts), transport)
			_, err := CallCustomMethod[*SubscribeEventParams, *SubscribeEventResult](ctx, cs, methodSubscribeEvent, &SubscribeEventParams{
				Name:     "comment.created",
				Delivery: &EventDeliveryTarget{Mode: "webhook", URL: "https://receiver.example.com/cb", Secret: "whsec_c2VjcmV0"},
			})
			var werr *jsonrpc.Error
			if !errors.As(err, &werr) {
				t.Fatalf("events/subscribe error %v (%T), want a *jsonrpc.Error", err, err)
			}
			if werr.Code != codeCallbackEndpoint {
				t.Errorf("error code %d, want %d", werr.Code, codeCallbackEndpoint)
			}
			if got, want := string(werr.Data), `{"reason":"challenge_failed"}`; got != want {
				t.Errorf("error data %s, want %s", got, want)
			}
		})
	}
}

func TestEventsMethodNotFoundWithoutHandlers(t *testing.T) {
	ctx := context.Background()
	for _, transport := range eventsTransports {
		t.Run(transport, func(t *testing.T) {
			cs := connectEvents(t, NewServer(testImpl, nil), transport)
			calls := map[string]func() error{
				methodListEvents: func() error {
					_, err := CallCustomMethod[*ListEventsParams, *ListEventsResult](ctx, cs, methodListEvents, &ListEventsParams{})
					return err
				},
				methodSubscribeEvent: func() error {
					_, err := CallCustomMethod[*SubscribeEventParams, *SubscribeEventResult](ctx, cs, methodSubscribeEvent, &SubscribeEventParams{Name: "comment.created"})
					return err
				},
				methodUnsubscribeEvent: func() error {
					_, err := CallCustomMethod[*UnsubscribeEventParams, *UnsubscribeEventResult](ctx, cs, methodUnsubscribeEvent, &UnsubscribeEventParams{Name: "comment.created"})
					return err
				},
			}
			for method, call := range calls {
				var werr *jsonrpc.Error
				if err := call(); !errors.As(err, &werr) || werr.Code != jsonrpc.CodeMethodNotFound {
					t.Errorf("%s: error %v, want code %d (method not found)", method, err, jsonrpc.CodeMethodNotFound)
				}
			}
		})
	}
}

// TestEventsTokenInfo checks that a handler behind
// StreamableHTTPOptions.TokenVerifier sees who subscribes.
func TestEventsTokenInfo(t *testing.T) {
	ctx := context.Background()
	rec := newEventsRecorder()
	server := NewServer(testImpl, eventsOptions(rec))
	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		if token != "test-token" {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: "owner:test", Expiration: time.Date(5000, 1, 2, 3, 4, 5, 0, time.UTC)}, nil
	}
	handler := NewStreamableHTTPHandler(func(*http.Request) *Server { return server }, &StreamableHTTPOptions{
		Stateless:     true,
		TokenVerifier: verifier,
	})
	httpServer := httptest.NewServer(mustNotPanic(t, handler))
	defer httpServer.Close()

	transport := &StreamableClientTransport{
		Endpoint:     httpServer.URL,
		OAuthHandler: &mockOAuthHandler{token: &oauth2.Token{AccessToken: "test-token"}},
	}
	cs, err := NewClient(testImpl, nil).Connect(ctx, transport, &ClientSessionOptions{ProtocolVersion: protocolVersion20260728})
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	if _, err := CallCustomMethod[*SubscribeEventParams, *SubscribeEventResult](ctx, cs, methodSubscribeEvent, &SubscribeEventParams{
		Name:     "comment.created",
		Delivery: &EventDeliveryTarget{Mode: "webhook", URL: "https://receiver.example.com/cb", Secret: "whsec_c2VjcmV0"},
	}); err != nil {
		t.Fatalf("events/subscribe: %v", err)
	}
	req := received(t, rec.subscribe)
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		t.Fatal("handler saw no token info")
	}
	if got := req.Extra.TokenInfo.UserID; got != "owner:test" {
		t.Errorf("handler saw user %q, want owner:test", got)
	}
	if req.Session == nil {
		t.Error("handler saw no session")
	}
}
