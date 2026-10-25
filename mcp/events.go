// Copyright 2025 The Go MCP SDK Authors. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package mcp

// This file serves the MCP events extension on the server side: the "events"
// capability and the events/list, events/subscribe and events/unsubscribe
// requests. The shapes follow the draft at
// https://github.com/modelcontextprotocol/experimental-ext-triggers-events
// as ChatGPT uses it (https://developers.openai.com/plugins/build/mcp-events).
// Delivering events, signing them and verifying callbacks are left to the
// server's handlers.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	internaljson "github.com/hessed-os/mcp-go-sdk/internal/json"
	"github.com/hessed-os/mcp-go-sdk/internal/jsonrpc2"
)

const (
	methodListEvents       = "events/list"
	methodSubscribeEvent   = "events/subscribe"
	methodUnsubscribeEvent = "events/unsubscribe"
)

// EventDefinition describes one event a client may subscribe to.
type EventDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Delivery lists the delivery modes the server supports for this event,
	// such as "webhook".
	Delivery []string `json:"delivery"`
	// InputSchema is the JSON Schema of the subscription arguments.
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
	// PayloadSchema is the JSON Schema of the data of each delivered event.
	PayloadSchema json.RawMessage `json:"payloadSchema,omitempty"`
}

// ListEventsParams are the parameters of an events/list request.
type ListEventsParams struct {
	// This property is reserved by the protocol to allow clients and servers to
	// attach additional metadata to their requests.
	Meta `json:"_meta,omitempty"`
	// An opaque token representing the current pagination position. If provided,
	// the server should return results starting after this cursor.
	Cursor string `json:"cursor,omitempty"`
}

func (x *ListEventsParams) isParams()              {}
func (x *ListEventsParams) isNil() bool            { return x == nil }
func (x *ListEventsParams) GetProgressToken() any  { return getProgressToken(x) }
func (x *ListEventsParams) SetProgressToken(t any) { setProgressToken(x, t) }
func (x *ListEventsParams) cursorPtr() *string     { return &x.Cursor }

// ListEventsResult is the server's response to an events/list request.
type ListEventsResult struct {
	// This property is reserved by the protocol to allow clients and servers to
	// attach additional metadata to their responses.
	Meta   `json:"_meta,omitempty"`
	Events []*EventDefinition `json:"events"`
	// An opaque token representing the pagination position after the last returned
	// result. If present, there may be more results available.
	NextCursor string `json:"nextCursor,omitempty"`
}

func (*ListEventsResult) isResult()                {}
func (x *ListEventsResult) nextCursorPtr() *string { return &x.NextCursor }

// EventDeliveryTarget says where and how the server delivers events of a
// subscription. For webhook delivery, Mode is "webhook", URL is the callback
// URL and Secret is the signing secret ("whsec_" followed by base64).
// events/unsubscribe carries no secret.
type EventDeliveryTarget struct {
	Mode   string `json:"mode"`
	URL    string `json:"url"`
	Secret string `json:"secret"`
}

// SubscribeEventParams are the parameters of an events/subscribe request,
// which creates or refreshes a subscription.
//
// The request's "ttlMs" has three meanings, which Go cannot hold in one
// pointer: absent asks for the server's default lifetime, null asks for no
// expiry, and a number asks for that lifetime in milliseconds. The custom JSON
// methods map them to TTLMs and NoExpiry:
//
//   - absent: TTLMs == nil and NoExpiry == false;
//   - null:   TTLMs == nil and NoExpiry == true;
//   - number: TTLMs != nil (NoExpiry is false).
type SubscribeEventParams struct {
	// This property is reserved by the protocol to allow clients and servers to
	// attach additional metadata to their requests.
	Meta `json:"_meta,omitempty"`
	// Name is the name of the event, as listed by events/list.
	Name string `json:"name"`
	// Arguments are the subscription arguments, described by the event's
	// InputSchema.
	Arguments json.RawMessage      `json:"arguments,omitempty"`
	Delivery  *EventDeliveryTarget `json:"delivery"`
	// Cursor is the replay position the client last saw; nil (JSON null)
	// when it has none.
	Cursor *string `json:"cursor"`
	// TTLMs is the requested lifetime in milliseconds; nil when "ttlMs" is
	// absent or null (see NoExpiry).
	TTLMs *int64 `json:"ttlMs,omitempty"`
	// NoExpiry reports that the request sent "ttlMs": null, asking for a
	// subscription that does not expire. It is ignored on encoding when
	// TTLMs is set.
	NoExpiry bool `json:"-"`
}

func (x *SubscribeEventParams) isParams()              {}
func (x *SubscribeEventParams) isNil() bool            { return x == nil }
func (x *SubscribeEventParams) GetProgressToken() any  { return getProgressToken(x) }
func (x *SubscribeEventParams) SetProgressToken(t any) { setProgressToken(x, t) }

func (x *SubscribeEventParams) MarshalJSON() ([]byte, error) {
	type params SubscribeEventParams // avoid recursion
	wire := struct {
		params
		TTLMs json.RawMessage `json:"ttlMs,omitempty"`
	}{params: params(*x)}
	switch {
	case x.TTLMs != nil:
		wire.TTLMs = strconv.AppendInt(nil, *x.TTLMs, 10)
	case x.NoExpiry:
		wire.TTLMs = json.RawMessage("null")
	}
	return json.Marshal(wire)
}

func (x *SubscribeEventParams) UnmarshalJSON(data []byte) error {
	type params SubscribeEventParams // avoid recursion
	var wire struct {
		params
		// Shadows params.TTLMs so that null and absent stay apart.
		TTLMs json.RawMessage `json:"ttlMs"`
	}
	if err := internaljson.Unmarshal(data, &wire); err != nil {
		return err
	}
	p := SubscribeEventParams(wire.params)
	p.TTLMs, p.NoExpiry = nil, false
	switch {
	case len(wire.TTLMs) == 0:
	case string(wire.TTLMs) == "null":
		p.NoExpiry = true
	default:
		var ms int64
		if err := internaljson.Unmarshal(wire.TTLMs, &ms); err != nil {
			return fmt.Errorf("ttlMs: %w", err)
		}
		p.TTLMs = &ms
	}
	*x = p
	return nil
}

// SubscribeEventResult is the server's response to an events/subscribe
// request.
type SubscribeEventResult struct {
	// This property is reserved by the protocol to allow clients and servers to
	// attach additional metadata to their responses.
	Meta `json:"_meta,omitempty"`
	// ID identifies the subscription.
	ID string `json:"id"`
	// RefreshBefore is when the granted subscription expires; nil only when
	// the server grants a request for no expiry.
	RefreshBefore *time.Time `json:"refreshBefore"`
	// Cursor is the replay position; nil for events without replay.
	Cursor *string `json:"cursor"`
	// Truncated reports that the requested history is no longer available.
	Truncated bool `json:"truncated"`
}

func (*SubscribeEventResult) isResult() {}

// UnsubscribeEventParams are the parameters of an events/unsubscribe request,
// which names the subscription by its event name, arguments and callback.
type UnsubscribeEventParams struct {
	// This property is reserved by the protocol to allow clients and servers to
	// attach additional metadata to their requests.
	Meta      `json:"_meta,omitempty"`
	Name      string               `json:"name"`
	Arguments json.RawMessage      `json:"arguments,omitempty"`
	Delivery  *EventDeliveryTarget `json:"delivery,omitempty"`
}

func (x *UnsubscribeEventParams) isParams()              {}
func (x *UnsubscribeEventParams) isNil() bool            { return x == nil }
func (x *UnsubscribeEventParams) GetProgressToken() any  { return getProgressToken(x) }
func (x *UnsubscribeEventParams) SetProgressToken(t any) { setProgressToken(x, t) }

// UnsubscribeEventResult is the server's (empty) response to an
// events/unsubscribe request.
type UnsubscribeEventResult struct {
	// This property is reserved by the protocol to allow clients and servers to
	// attach additional metadata to their responses.
	Meta `json:"_meta,omitempty"`
}

func (*UnsubscribeEventResult) isResult() {}

// checkEventHandlers panics unless the three events handlers of opts are all
// set or all unset.
func checkEventHandlers(opts *ServerOptions) {
	handlers := []struct {
		name string
		set  bool
	}{
		{"ListEventsHandler", opts.ListEventsHandler != nil},
		{"SubscribeEventHandler", opts.SubscribeEventHandler != nil},
		{"UnsubscribeEventHandler", opts.UnsubscribeEventHandler != nil},
	}
	for _, h := range handlers {
		if !h.set {
			continue
		}
		for _, other := range handlers {
			if !other.set {
				panic(h.name + " requires " + other.name)
			}
		}
	}
}

var errEventsNotSupported = fmt.Errorf("%w: server does not support events", jsonrpc2.ErrMethodNotFound)

func (s *Server) listEvents(ctx context.Context, req *ServerRequest[*ListEventsParams]) (*ListEventsResult, error) {
	if s.opts.ListEventsHandler == nil {
		return nil, errEventsNotSupported
	}
	return s.opts.ListEventsHandler(ctx, req)
}

func (s *Server) subscribeEvent(ctx context.Context, req *ServerRequest[*SubscribeEventParams]) (*SubscribeEventResult, error) {
	if s.opts.SubscribeEventHandler == nil {
		return nil, errEventsNotSupported
	}
	return s.opts.SubscribeEventHandler(ctx, req)
}

func (s *Server) unsubscribeEvent(ctx context.Context, req *ServerRequest[*UnsubscribeEventParams]) (*UnsubscribeEventResult, error) {
	if s.opts.UnsubscribeEventHandler == nil {
		return nil, errEventsNotSupported
	}
	return s.opts.UnsubscribeEventHandler(ctx, req)
}
