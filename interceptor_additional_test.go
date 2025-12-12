package fins

import (
	"bytes"
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoggingInterceptor(t *testing.T) {
	ctx := context.Background()
	info := &InterceptorInfo{
		Operation:  OpReadWords,
		MemoryArea: MemoryAreaDMWord,
		Address:    42,
		Count:      2,
	}

	// Success case
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	_, err := LoggingInterceptor(logger)(ctx, info, func(context.Context) (interface{}, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "Starting ReadWords") || !strings.Contains(logged, "Completed ReadWords") {
		t.Fatalf("unexpected log output: %q", logged)
	}

	// Error case
	buf.Reset()
	_, err = LoggingInterceptor(logger)(ctx, info, func(context.Context) (interface{}, error) {
		return nil, errors.New("boom")
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	logged = buf.String()
	if !strings.Contains(logged, "Failed ReadWords") || !strings.Contains(logged, "boom") {
		t.Fatalf("unexpected log output on error: %q", logged)
	}
}

func TestMetricsCollectorConcurrency(t *testing.T) {
	ctx := context.Background()
	collector := NewMetricsCollector()
	interceptor := collector.Interceptor()

	var wg sync.WaitGroup
	const successCalls = 10
	for i := 0; i < successCalls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = interceptor(ctx, &InterceptorInfo{Operation: OpReadWords}, func(context.Context) (interface{}, error) {
				time.Sleep(1 * time.Millisecond)
				return "ok", nil
			})
		}()
	}

	const errorCalls = 3
	for i := 0; i < errorCalls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = interceptor(ctx, &InterceptorInfo{Operation: OpWriteWords}, func(context.Context) (interface{}, error) {
				return nil, errors.New("fail")
			})
		}()
	}

	wg.Wait()

	count, errorsCount, avg := collector.GetStats(OpReadWords)
	if count != successCalls || errorsCount != 0 {
		t.Fatalf("unexpected read stats: count=%d errors=%d", count, errorsCount)
	}
	if avg <= 0 {
		t.Fatalf("expected average duration to be recorded")
	}

	count, errorsCount, _ = collector.GetStats(OpWriteWords)
	if count != errorCalls || errorsCount != errorCalls {
		t.Fatalf("unexpected write stats: count=%d errors=%d", count, errorsCount)
	}

	collector.Reset()
	count, errorsCount, _ = collector.GetStats(OpWriteWords)
	if count != 0 || errorsCount != 0 {
		t.Fatalf("reset should clear metrics, got count=%d errors=%d", count, errorsCount)
	}
}

func TestValidationInterceptorWithLimits(t *testing.T) {
	ctx := context.Background()
	validator := ValidationInterceptorWithLimits(2, 2)

	// Valid read should reach invoker
	called := false
	_, err := validator(ctx, &InterceptorInfo{
		Operation:  OpReadWords,
		Count:      1,
		MemoryArea: MemoryAreaDMWord,
	}, func(context.Context) (interface{}, error) {
		called = true
		return "ok", nil
	})
	if err != nil || !called {
		t.Fatalf("validator blocked valid operation: called=%v err=%v", called, err)
	}

	tests := []struct {
		name string
		info *InterceptorInfo
	}{
		{"zero read count", &InterceptorInfo{Operation: OpReadWords, Count: 0}},
		{"exceeds read limit", &InterceptorInfo{Operation: OpReadWords, Count: 5}},
		{"invalid write words type", &InterceptorInfo{Operation: OpWriteWords, Data: []byte{1}}},
		{"empty write bytes", &InterceptorInfo{Operation: OpWriteBytes, Data: []byte{}}},
		{"write bytes too large", &InterceptorInfo{Operation: OpWriteBytes, Data: make([]byte, 10), Count: 0, MemoryArea: MemoryAreaDMWord}},
		{"write bits too many", &InterceptorInfo{Operation: OpWriteBits, Data: make([]bool, 33)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validator(ctx, tt.info, func(context.Context) (interface{}, error) {
				return nil, nil
			})
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.name)
			}
		})
	}
}

func TestAddressRangeAndReadOnlyInterceptors(t *testing.T) {
	ctx := context.Background()
	validator := AddressRangeValidator(map[byte]struct{ Min, Max uint16 }{
		MemoryAreaDMWord: {Min: 0, Max: 10},
	})
	readOnly := ReadOnlyInterceptor()

	// Valid read passes both
	_, err := validator(ctx, &InterceptorInfo{Operation: OpReadWords, MemoryArea: MemoryAreaDMWord, Address: 5, Count: 1}, func(context.Context) (interface{}, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("unexpected error from address validator: %v", err)
	}

	_, err = readOnly(ctx, &InterceptorInfo{Operation: OpReadWords}, func(context.Context) (interface{}, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("read-only should allow reads: %v", err)
	}

	// Invalid area
	_, err = validator(ctx, &InterceptorInfo{Operation: OpReadWords, MemoryArea: MemoryAreaDMBit, Address: 5, Count: 1}, func(context.Context) (interface{}, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatalf("expected area validation error")
	}

	// Address overflow
	_, err = validator(ctx, &InterceptorInfo{Operation: OpReadWords, MemoryArea: MemoryAreaDMWord, Address: 10, Count: 2}, func(context.Context) (interface{}, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatalf("expected address overflow error")
	}

	// Write should be blocked
	_, err = readOnly(ctx, &InterceptorInfo{Operation: OpWriteWords}, func(context.Context) (interface{}, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatalf("expected read-only interceptor to block write")
	}
}

func TestRetryInterceptors(t *testing.T) {
	ctx := context.Background()

	// Basic retry until success
	attempts := 0
	result, err := RetryInterceptor(2, 0)(ctx, &InterceptorInfo{Operation: OpReadWords}, func(context.Context) (interface{}, error) {
		if attempts < 2 {
			attempts++
			return nil, errors.New("boom")
		}
		return "ok", nil
	})
	if err != nil || result != "ok" || attempts != 2 {
		t.Fatalf("retry interceptor failed: result=%v err=%v attempts=%d", result, err, attempts)
	}

	// Respect canceled context (should not retry)
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	attempts = 0
	_, err = RetryInterceptor(3, 0)(cancelCtx, &InterceptorInfo{Operation: OpReadWords}, func(context.Context) (interface{}, error) {
		attempts++
		return nil, errors.New("boom")
	})
	if err == nil || attempts != 1 {
		t.Fatalf("expected single attempt due to canceled context, attempts=%d err=%v", attempts, err)
	}

	// Conditional retry
	attempts = 0
	_, err = RetryInterceptorConditional(3, 0, func(err error) bool { return false })(ctx, &InterceptorInfo{Operation: OpReadWords}, func(context.Context) (interface{}, error) {
		attempts++
		return nil, errors.New("boom")
	})
	if err == nil || attempts != 1 {
		t.Fatalf("conditional retry should stop when shouldRetry returns false, attempts=%d err=%v", attempts, err)
	}

	attempts = 0
	result, err = RetryInterceptorWithBackoff(2, 0, 1*time.Millisecond)(ctx, &InterceptorInfo{Operation: OpReadWords}, func(context.Context) (interface{}, error) {
		if attempts == 0 {
			attempts++
			return nil, errors.New("boom")
		}
		return "ok", nil
	})
	if err != nil || result != "ok" || attempts != 1 {
		t.Fatalf("backoff retry failed: result=%v err=%v attempts=%d", result, err, attempts)
	}
}

func TestChainInterceptorsOrder(t *testing.T) {
	ctx := context.Background()
	info := &InterceptorInfo{Operation: OpReadWords}
	order := make([]string, 0, 5)

	i1 := func(ctx context.Context, info *InterceptorInfo, invoker Invoker) (interface{}, error) {
		order = append(order, "i1-start")
		res, err := invoker(ctx)
		order = append(order, "i1-end")
		return res, err
	}
	i2 := func(ctx context.Context, info *InterceptorInfo, invoker Invoker) (interface{}, error) {
		order = append(order, "i2-start")
		res, err := invoker(ctx)
		order = append(order, "i2-end")
		return res, err
	}

	result, err := ChainInterceptors(i1, i2)(ctx, info, func(context.Context) (interface{}, error) {
		order = append(order, "invoker")
		return "ok", nil
	})
	if err != nil || result != "ok" {
		t.Fatalf("chain interceptors error: result=%v err=%v", result, err)
	}

	expected := []string{"i1-start", "i2-start", "invoker", "i2-end", "i1-end"}
	if len(order) != len(expected) {
		t.Fatalf("unexpected order length: %v", order)
	}
	for i := range expected {
		if order[i] != expected[i] {
			t.Fatalf("unexpected order at %d: got %s want %s", i, order[i], expected[i])
		}
	}
}
