package api

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/abevz/dibs/internal/core"
)

type mutationCounters struct {
	mu                  sync.Mutex
	claimConflicts      int64
	leaseLossFailures   int64
	transactionFailures int64
	dbBusyFailures      int64
	staleRejections     int64
}

func (c *mutationCounters) snapshot() core.MutationCounters {
	c.mu.Lock()
	defer c.mu.Unlock()
	return core.MutationCounters{
		ClaimConflicts:      c.claimConflicts,
		LeaseLossFailures:   c.leaseLossFailures,
		TransactionFailures: c.transactionFailures,
		DBBusyFailures:      c.dbBusyFailures,
		StaleRejections:     c.staleRejections,
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func observeMutations(next http.Handler, logger *slog.Logger, counters *mutationCounters) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		wrapped := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)
		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}
		code := w.Header().Get("X-Dibs-Result-Code")
		if code == "" {
			code = w.Header().Get("X-Dibs-Error-Code")
		}
		if code == "" {
			code = "ok"
		}
		operation := r.Pattern
		if operation == "" {
			operation = r.Method + " unknown"
		}
		counters.mu.Lock()
		if strings.Contains(operation, "/claim") && (code == core.ErrLeaseHeld || code == core.ErrIssueNotReady) {
			counters.claimConflicts++
		}
		if code == core.ErrLeaseExpired {
			counters.leaseLossFailures++
			counters.staleRejections++
		}
		if code == core.ErrConflict {
			counters.staleRejections++
		}
		if code == "db_busy" {
			counters.dbBusyFailures++
		}
		if code == "db_busy" || code == "transaction_failure" {
			counters.transactionFailures++
		}
		counters.mu.Unlock()
		logger.Info("mutation result", "operation", operation, "result_code", code,
			"status", status, "latency_ms", time.Since(started).Milliseconds())
	})
}
