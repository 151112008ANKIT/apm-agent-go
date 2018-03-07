package trace_test

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/elastic/apm-agent-go/model"
	"github.com/elastic/apm-agent-go/trace"
	"github.com/elastic/apm-agent-go/transport/transporttest"
)

func TestTracerStats(t *testing.T) {
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()
	tracer.Transport = transporttest.Discard

	for i := 0; i < 500; i++ {
		tracer.StartTransaction("name", "type").Done(-1)
	}
	tracer.Flush(nil)
	assert.Equal(t, trace.TracerStats{
		TransactionsSent: 500,
	}, tracer.Stats())
}

func TestTracerClosedSendNonblocking(t *testing.T) {
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	tracer.Close()

	for i := 0; i < 1001; i++ {
		tracer.StartTransaction("name", "type").Done(-1)
	}
	assert.Equal(t, uint64(1), tracer.Stats().TransactionsDropped)
}

func TestTracerFlushInterval(t *testing.T) {
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()
	tracer.Transport = transporttest.Discard

	interval := time.Second
	tracer.SetFlushInterval(interval)

	before := time.Now()
	tracer.StartTransaction("name", "type").Done(-1)
	assert.Equal(t, trace.TracerStats{TransactionsSent: 0}, tracer.Stats())
	for tracer.Stats().TransactionsSent == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	assert.WithinDuration(t, before.Add(interval), time.Now(), 100*time.Millisecond)
}

func TestTracerMaxQueueSize(t *testing.T) {
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()

	// Prevent any transactions from being sent.
	tracer.Transport = transporttest.ErrorTransport{Error: errors.New("nope")}

	// Enqueue 10 transactions with a queue size of 5;
	// we should see 5 transactons dropped.
	tracer.SetMaxTransactionQueueSize(5)
	for i := 0; i < 10; i++ {
		tracer.StartTransaction("name", "type").Done(-1)
	}
	for tracer.Stats().TransactionsDropped < 5 {
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, trace.TracerStats{
		Errors: trace.TracerStatsErrors{
			SendTransactions: 1,
		},
		TransactionsDropped: 5,
	}, tracer.Stats())
}

func TestTracerRetryTimer(t *testing.T) {
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()

	// Prevent any transactions from being sent.
	tracer.Transport = transporttest.ErrorTransport{Error: errors.New("nope")}

	interval := time.Second
	tracer.SetFlushInterval(interval)
	tracer.SetMaxTransactionQueueSize(1)

	before := time.Now()
	tracer.StartTransaction("name", "type").Done(-1)
	for tracer.Stats().Errors.SendTransactions < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, trace.TracerStats{
		Errors: trace.TracerStatsErrors{
			SendTransactions: 1,
		},
	}, tracer.Stats())

	// Send another transaction, which should cause the
	// existing transaction to be dropped, but should not
	// preempt the retry timer.
	tracer.StartTransaction("name", "type").Done(-1)
	for tracer.Stats().Errors.SendTransactions < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	assert.WithinDuration(t, before.Add(interval), time.Now(), 100*time.Millisecond)
	assert.Equal(t, trace.TracerStats{
		Errors: trace.TracerStatsErrors{
			SendTransactions: 2,
		},
		TransactionsDropped: 1,
	}, tracer.Stats())
}

func TestTracerMaxSpans(t *testing.T) {
	var r transporttest.RecorderTransport
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()
	tracer.Transport = &r

	tracer.SetMaxSpans(2)
	tx := tracer.StartTransaction("name", "type")
	// SetMaxSpans only affects transactions started
	// after the call.
	tracer.SetMaxSpans(99)

	s0 := tx.StartSpan("name", "type", nil)
	s1 := tx.StartSpan("name", "type", nil)
	s2 := tx.StartSpan("name", "type", nil)
	tx.Done(-1)

	assert.False(t, s0.Dropped())
	assert.False(t, s1.Dropped())
	assert.True(t, s2.Dropped())

	tracer.Flush(nil)
	payloads := r.Payloads()
	assert.Len(t, payloads, 1)
	transactions := payloads[0]["transactions"].([]interface{})
	assert.Len(t, transactions, 1)
	transaction := transactions[0].(map[string]interface{})
	assert.Len(t, transaction["spans"], 2)
}

func TestTracerErrors(t *testing.T) {
	var r transporttest.RecorderTransport
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()
	tracer.Transport = &r

	error_ := tracer.NewError()
	error_.SetException(&testError{
		"zing", newErrorsStackTrace(0, 2),
	})
	error_.Send()
	tracer.Flush(nil)

	payloads := r.Payloads()
	assert.Len(t, payloads, 1)
	errors := payloads[0]["errors"].([]interface{})
	assert.Len(t, errors, 1)
	exception := errors[0].(map[string]interface{})["exception"].(map[string]interface{})
	assert.Equal(t, "zing", exception["message"])
	assert.Equal(t, "github.com/elastic/apm-agent-go/trace_test", exception["module"])
	assert.Equal(t, "testError", exception["type"])
	stacktrace := exception["stacktrace"].([]interface{})
	assert.Len(t, stacktrace, 2)
	frame0 := stacktrace[0].(map[string]interface{})
	frame1 := stacktrace[1].(map[string]interface{})
	assert.Equal(t, "newErrorsStackTrace", frame0["function"])
	assert.Equal(t, "TestTracerErrors", frame1["function"])
}

func TestTracerErrorsBuffered(t *testing.T) {
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()
	errors := make(chan transporttest.SendErrorsRequest)
	tracer.Transport = &transporttest.ChannelTransport{Errors: errors}

	tracer.SetMaxErrorQueueSize(10)
	sendError := func(msg string) {
		e := tracer.NewError()
		e.SetException(fmt.Errorf("%s", msg))
		e.Send()
	}

	// Send an initial error, which should send a request
	// on the transport's errors channel.
	sendError("0")
	var req transporttest.SendErrorsRequest
	select {
	case req = <-errors:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for errors payload")
	}
	assert.Len(t, req.Payload.Errors, 1)

	// While we're still sending the first error, try to
	// enqueue another 1010. The first 1000 should be
	// buffered in the channel, but the internal queue
	// will not be filled until the send has completed,
	// so the additional 10 will be dropped.
	for i := 1; i <= 1010; i++ {
		sendError(fmt.Sprint(i))
	}
	req.Result <- fmt.Errorf("nope")

	stats := tracer.Stats()
	assert.Equal(t, stats.ErrorsDropped, uint64(10))

	// The tracer should send 100 lots of 10 errors.
	for i := 0; i < 100; i++ {
		select {
		case req = <-errors:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for errors payload")
		}
		assert.Len(t, req.Payload.Errors, 10)
		for j, e := range req.Payload.Errors {
			assert.Equal(t, e.Exception.Message, fmt.Sprintf("%d", i*10+j))
		}
		req.Result <- nil
	}
}

func TestTracerProcessor(t *testing.T) {
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()
	tracer.Transport = transporttest.Discard

	e_ := tracer.NewError()
	tx_ := tracer.StartTransaction("name", "type")

	var processedError bool
	var processedTransaction bool
	processError := func(e *model.Error) {
		processedError = true
		assert.Equal(t, &e_.Error, e)
	}
	processTransaction := func(tx *model.Transaction) {
		processedTransaction = true
		assert.Equal(t, &tx_.Transaction, tx)
	}
	tracer.SetProcessor(struct {
		trace.ErrorProcessor
		trace.TransactionProcessor
	}{
		trace.ErrorProcessorFunc(processError),
		trace.TransactionProcessorFunc(processTransaction),
	})

	e_.Send()
	tx_.Done(-1)
	tracer.Flush(nil)
	assert.True(t, processedError)
	assert.True(t, processedTransaction)
}

func TestTracerRecover(t *testing.T) {
	var r transporttest.RecorderTransport
	tracer, err := trace.NewTracer("tracer.testing", "")
	assert.NoError(t, err)
	defer tracer.Close()
	tracer.Transport = &r

	capturePanic(tracer, "blam")
	tracer.Flush(nil)

	payloads := r.Payloads()
	assert.Len(t, payloads, 2)

	assert.Contains(t, payloads[0], "errors")
	errors := payloads[0]["errors"].([]interface{})
	assert.Len(t, errors, 1)
	error0 := errors[0].(map[string]interface{})
	exception := error0["exception"].(map[string]interface{})
	assert.Equal(t, "blam", exception["message"])

	assert.Contains(t, payloads[1], "transactions")
	transactions := payloads[1]["transactions"].([]interface{})
	assert.Len(t, transactions, 1)
	transaction0 := transactions[0].(map[string]interface{})

	errorTransaction := error0["transaction"].(map[string]interface{})
	assert.Equal(t, transaction0["id"], errorTransaction["id"])
}

func capturePanic(tracer *trace.Tracer, v interface{}) {
	tx := tracer.StartTransaction("name", "type")
	defer tx.Done(-1)
	defer tracer.Recover(tx)
	panic(v)
}

type testLogger struct {
	t *testing.T
}

func (l testLogger) Debugf(format string, args ...interface{}) {
	l.t.Logf("[DEBUG] "+format, args...)
}

func (l testLogger) Errorf(format string, args ...interface{}) {
	l.t.Logf("[ERROR] "+format, args...)
}

type testError struct {
	message    string
	stackTrace errors.StackTrace
}

func (e *testError) Error() string {
	return e.message
}

func (e *testError) StackTrace() errors.StackTrace {
	return e.stackTrace
}

func newErrorsStackTrace(skip, n int) errors.StackTrace {
	callers := make([]uintptr, 2)
	callers = callers[:runtime.Callers(1, callers)]
	frames := make([]errors.Frame, len(callers))
	for i, pc := range callers {
		frames[i] = errors.Frame(pc)
	}
	return errors.StackTrace(frames)
}
