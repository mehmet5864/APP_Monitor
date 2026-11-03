// Package cdp Chrome DevTools Protocol için dış bağımlılıksız küçük bir istemci ve Chromium başlatıcısıdır.
package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

type Client struct {
	ws       *wsConn
	nextID   atomic.Int64
	mu       sync.Mutex
	pending  map[int64]chan callResult
	handlers map[string][]func(json.RawMessage)
	done     chan struct{}
	err      error
}

type callResult struct {
	data json.RawMessage
	err  error
}

// NewClient bir sayfanın DevTools WebSocket adresine bağlanır.
func NewClient(ctx context.Context, wsURL string) (*Client, error) {
	ws, err := dialWS(ctx, wsURL)
	if err != nil {
		return nil, err
	}
	c := &Client{ws: ws, pending: map[int64]chan callResult{}, handlers: map[string][]func(json.RawMessage){}, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

func (c *Client) readLoop() {
	defer close(c.done)
	for {
		raw, err := c.ws.readMessage()
		if err != nil {
			c.mu.Lock()
			c.err = err
			for id, ch := range c.pending {
				ch <- callResult{err: fmt.Errorf("tarayıcı bağlantısı koptu")}
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var m struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				if m.Error != nil {
					ch <- callResult{err: errors.New(m.Error.Message)}
				} else {
					ch <- callResult{data: m.Result}
				}
			}
			continue
		}
		c.mu.Lock()
		hs := append([]func(json.RawMessage){}, c.handlers[m.Method]...)
		c.mu.Unlock()
		for _, h := range hs {
			h(m.Params) // olay işleyicileri hızlı dönmeli; Call çağıracaksa goroutine açmalı
		}
	}
}

// On bir CDP olayı için işleyici ekler.
func (c *Client) On(method string, fn func(json.RawMessage)) {
	c.mu.Lock()
	c.handlers[method] = append(c.handlers[method], fn)
	c.mu.Unlock()
}

// Call bir CDP komutu çalıştırır; sonucu out'a (boş değilse) çözer.
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := c.nextID.Add(1)
	ch := make(chan callResult, 1)
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return errors.New("tarayıcı bağlantısı kapalı")
	}
	c.pending[id] = ch
	c.mu.Unlock()
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return err
	}
	if err := c.ws.writeText(b); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		if out != nil && len(r.data) > 0 {
			return json.Unmarshal(r.data, out)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.done:
		return errors.New("tarayıcı bağlantısı koptu")
	}
}

func (c *Client) Close() { c.ws.close() }

// Done bağlantı kapanınca kapanan kanaldır.
func (c *Client) Done() <-chan struct{} { return c.done }
