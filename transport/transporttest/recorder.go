package transporttest

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/elastic/apm-agent-go/internal/fastjson"
	"github.com/elastic/apm-agent-go/model"
)

// RecorderTransport implements transport.Transport,
// recording the payloads sent. The payloads can be
// retrieved using the Payloads method.
type RecorderTransport struct {
	mu       sync.Mutex
	payloads []map[string]interface{}
}

// SendTransactions records the transactions payload such that it can later be
// obtained via Payloads.
func (r *RecorderTransport) SendTransactions(ctx context.Context, payload *model.TransactionsPayload) error {
	return r.record(payload)
}

// SendErrors records the errors payload such that it can later be obtained via
// Payloads.
func (r *RecorderTransport) SendErrors(ctx context.Context, payload *model.ErrorsPayload) error {
	return r.record(payload)
}

// Payloads returns the payloads recorded by SendTransactions and SendErrors.
func (r *RecorderTransport) Payloads() []map[string]interface{} {
	r.mu.Lock()
	payloads := r.payloads[:]
	r.mu.Unlock()
	return payloads
}

func (r *RecorderTransport) record(payload interface{}) error {
	var w fastjson.Writer
	fastjson.Marshal(&w, payload)
	var m map[string]interface{}
	if err := json.Unmarshal(w.Bytes(), &m); err != nil {
		panic(err)
	}
	r.mu.Lock()
	r.payloads = append(r.payloads, m)
	r.mu.Unlock()
	return nil
}
