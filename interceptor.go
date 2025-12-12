package fins

import "context"

// OperationType represents the type of FINS operation
type OperationType string

const (
	OpReadWords   OperationType = "ReadWords"
	OpReadBytes   OperationType = "ReadBytes"
	OpReadString  OperationType = "ReadString"
	OpReadBits    OperationType = "ReadBits"
	OpReadClock   OperationType = "ReadClock"
	OpWriteWords  OperationType = "WriteWords"
	OpWriteBytes  OperationType = "WriteBytes"
	OpWriteString OperationType = "WriteString"
	OpWriteBits   OperationType = "WriteBits"
	OpSetBit      OperationType = "SetBit"
	OpResetBit    OperationType = "ResetBit"
	OpToggleBit   OperationType = "ToggleBit"
)

// InterceptorInfo contains information about the operation being performed
type InterceptorInfo struct {
	Operation  OperationType
	MemoryArea byte
	Address    uint16
	BitOffset  byte        // Only for bit operations
	Count      uint16      // For read operations
	Data       interface{} // For write operations ([]uint16, []byte, []bool, string, etc.)
}

// Invoker is a function that executes the actual operation
type Invoker func(ctx context.Context) (interface{}, error)

// Interceptor is a function that can intercept and wrap FINS operations
// It receives:
//   - ctx: the context for the operation
//   - info: information about the operation being performed
//   - invoker: function to call the actual operation
//
// The interceptor can:
//   - Log the operation
//   - Measure timing/metrics
//   - Add tracing
//   - Modify the context
//   - Handle errors
//   - Short-circuit the operation
//
// Example:
//
//	func loggingInterceptor(ctx context.Context, info *InterceptorInfo, invoker Invoker) (interface{}, error) {
//	    start := time.Now()
//	    log.Printf("Starting %s at address %d", info.Operation, info.Address)
//
//	    result, err := invoker(ctx)
//
//	    log.Printf("Finished %s in %v, err: %v", info.Operation, time.Since(start), err)
//	    return result, err
//	}
type Interceptor func(ctx context.Context, info *InterceptorInfo, invoker Invoker) (interface{}, error)

// ChainInterceptors chains multiple interceptors into a single interceptor
// Interceptors are executed in order: first interceptor wraps second, second wraps third, etc.
func ChainInterceptors(interceptors ...Interceptor) Interceptor {
	if len(interceptors) == 0 {
		return nil
	}

	if len(interceptors) == 1 {
		return interceptors[0]
	}

	return func(ctx context.Context, info *InterceptorInfo, invoker Invoker) (interface{}, error) {
		return interceptors[0](ctx, info, func(ctx context.Context) (interface{}, error) {
			return ChainInterceptors(interceptors[1:]...)(ctx, info, invoker)
		})
	}
}
