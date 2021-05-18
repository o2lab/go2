package protocol

import (
	"bytes"
	"context"

	"github.com/april1989/origin-go-tools/internal/event"
	"github.com/april1989/origin-go-tools/internal/event/core"
	"github.com/april1989/origin-go-tools/internal/event/export"
	"github.com/april1989/origin-go-tools/internal/event/label"
	"github.com/april1989/origin-go-tools/internal/xcontext"
)

type contextKey int

const (
	clientKey = contextKey(iota)
)

func WithClient(ctx context.Context, client Client) context.Context {
	return context.WithValue(ctx, clientKey, client)
}

func LogEvent(ctx context.Context, ev core.Event, tags label.Map) context.Context {
	if !event.IsLog(ev) {
		return ctx
	}
	client, ok := ctx.Value(clientKey).(Client)
	if !ok {
		return ctx
	}
	buf := &bytes.Buffer{}
	p := export.Printer{}
	p.WriteEvent(buf, ev, tags)
	msg := &LogMessageParams{Type: Info, Message: buf.String()}
	if event.IsError(ev) {
		msg.Type = Error
	}
	go client.LogMessage(xcontext.Detach(ctx), msg)
	return ctx
}
