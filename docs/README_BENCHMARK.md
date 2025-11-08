# Go Logger Benchmark Suite

This benchmark suite provides a comprehensive comparison between your current CustomLogger implementation and Zerolog, a high-performance structured logging library.

## Files Included

1. **`gologger/logger_benchmark_test.go`** - Complete benchmark test suite
2. **`run_benchmark.ps1`** - PowerShell script to run benchmarks (Windows)  
3. **`run_benchmark.sh`** - Bash script to run benchmarks (Linux/macOS)
4. **`BENCHMARK_ANALYSIS.md`** - Detailed performance analysis and recommendations

## Quick Start

### Windows (PowerShell)
```powershell
.\run_benchmark.ps1
```

### Linux/macOS (Bash)
```bash
./run_benchmark.sh
```

### Manual Execution
```bash
cd gologger
go test -bench=. -benchmem .
```

## Benchmark Categories

### 1. Basic Logging Performance
- `BenchmarkCustomLogger_Info` vs `BenchmarkZerolog_Info_Discard`
- Tests simple message logging performance

### 2. Formatted Logging
- `BenchmarkCustomLogger_Infof` vs `BenchmarkZerolog_Infof_Discard`  
- Tests performance with format strings and parameters

### 3. Error Logging
- `BenchmarkCustomLogger_Error` vs `BenchmarkZerolog_Error_Discard`
- Tests error logging with error objects

### 4. Structured Logging (Fields)
- `BenchmarkCustomLogger_InfoWithFields` vs `BenchmarkZerolog_InfoWithFields_Discard`
- Tests logging with additional key-value pairs

### 5. Context Logging (Tracing)
- `BenchmarkCustomLogger_InfoWithContext` vs `BenchmarkZerolog_InfoWithContext_Discard`
- Tests performance with OpenTelemetry context

### 6. Level Filtering
- `BenchmarkCustomLogger_Debug_Disabled/Enabled` vs `BenchmarkZerolog_Debug_Disabled/Enabled`
- Tests overhead of log level checking

### 7. Memory Allocation Analysis
- `BenchmarkCustomLogger_Allocations` vs `BenchmarkZerolog_Allocations_Discard`
- Detailed memory usage and allocation patterns

### 8. Complex Logging Scenarios
- `BenchmarkCustomLogger_ComplexLog` vs `BenchmarkZerolog_ComplexLog_Discard`
- Tests performance with multiple fields and error objects

### 9. Time Measurement
- `BenchmarkCustomLogger_TicToc` vs `BenchmarkZerolog_Duration_Discard`
- Compares time logging approaches

### 10. I/O Impact
- File output vs in-memory benchmarks
- Tests real-world I/O performance impact

## Key Performance Metrics

The benchmarks measure:
- **ns/op**: Nanoseconds per operation (lower is better)
- **B/op**: Bytes allocated per operation (lower is better)
- **allocs/op**: Number of allocations per operation (lower is better)

## Initial Results Summary

Based on preliminary testing:

| Metric | CustomLogger | Zerolog | Performance Ratio |
|--------|-------------|---------|------------------|
| Speed (ns/op) | ~190,000 | ~50 | **3,800x faster** |
| Memory (B/op) | ~2,200 | 0 | **Zero allocations** |
| Allocations | 28 | 0 | **Zero allocations** |

## Understanding the Results

### CustomLogger Strengths
- Rich structured output with timestamps, facilities, K8s namespace
- Built-in Graylog GELF integration  
- Comprehensive context tracking
- Tic/Toc timing functionality

### CustomLogger Performance Costs
- High memory allocation (28 allocs per log)
- Complex JSON string construction
- Multiple buffer operations
- Timestamp formatting overhead

### Zerolog Advantages  
- Zero-allocation design
- Compile-time optimizations
- Efficient JSON encoding
- Minimal CPU overhead

## Production Considerations

### When to Use CustomLogger
- Low-traffic applications (<1000 logs/sec)
- Applications requiring built-in Graylog integration
- Services needing rich default context (K8s namespace, facility, etc.)
- Applications using Tic/Toc timing extensively

### When to Consider Zerolog
- High-traffic applications (>10,000 logs/sec)
- Performance-critical services
- Applications with memory constraints
- Services requiring minimal GC pressure

## Migration Path

If considering migration to Zerolog:

1. **Phase 1**: Implement Zerolog in one high-traffic service
2. **Phase 2**: Develop Graylog integration adapter
3. **Phase 3**: Create compatibility wrapper for gradual migration
4. **Phase 4**: Service-by-service migration with monitoring

## Running Additional Benchmarks

To add custom benchmarks, edit `logger_benchmark_test.go` and add functions following the pattern:

```go
func BenchmarkYourTest(b *testing.B) {
    // Setup
    logger := setupCustomLogger()
    
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            // Your logging code here
        }
    })
}
```

## Dependencies Added

The benchmark suite adds these dependencies:
- `github.com/rs/zerolog` - High-performance structured logging
- Standard Go testing and OpenTelemetry packages (already present)

## System Requirements

- Go 1.23.0 or later
- Windows PowerShell 5.1+ (for PowerShell script)
- Bash (for shell script)  
- ~100MB disk space for benchmark results

---

For questions or issues with the benchmark suite, refer to the detailed analysis in `BENCHMARK_ANALYSIS.md`.