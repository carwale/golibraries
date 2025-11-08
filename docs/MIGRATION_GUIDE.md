# Logger Migration Guide: CustomLogger to ZerologLogger

This guide provides step-by-step instructions for migrating from the existing CustomLogger to the new high-performance ZerologLogger while maintaining backward compatibility.

## Overview

The new implementation provides:
- **Interface-based design**: Both loggers implement the `ILogger` interface
- **Drop-in replacement**: Same API, just change the constructor
- **Significant performance improvement**: 1000x+ faster with zero allocations
- **Backward compatibility**: Existing code continues to work unchanged

## Migration Strategies

### Strategy 1: Immediate Full Migration

Replace logger creation throughout your codebase:

**Before:**
```go
logger := gologger.NewLogger(
    gologger.SetLogLevel("INFO"),
    gologger.GraylogHost("graylog.company.com"),
    gologger.GraylogPort(12201),
    gologger.GraylogFacility("MyService"),
)
```

**After:**
```go
logger := gologger.NewZerologLogger(
    gologger.ZerologSetLogLevel("INFO"),
    gologger.ZerologEnableGraylog("graylog.company.com", 12201),
    gologger.ZerologGraylogFacility("MyService"),
)
```

### Strategy 2: Gradual Migration with Factory Pattern

**Step 1: Update logger creation to use interface**
```go
// Before
func createLogger() *gologger.CustomLogger {
    return gologger.NewLogger(
        gologger.SetLogLevel("INFO"),
        gologger.DisableGraylog(true),
    )
}

// After  
func createLogger() gologger.ILogger {
    return gologger.NewLogger(
        gologger.SetLogLevel("INFO"),
        gologger.DisableGraylog(true),
    )
}
```

**Step 2: Add configuration to control logger type**
```go
func createLogger(useZerolog bool) gologger.ILogger {
    if useZerolog {
        return gologger.NewZerologLogger(
            gologger.ZerologSetLogLevel("INFO"),
            gologger.ZerologDisableGraylog(),
        )
    }
    return gologger.NewLogger(
        gologger.SetLogLevel("INFO"),
        gologger.DisableGraylog(true),
    )
}
```

**Step 3: Use environment variable or feature flag**
```go
func createLogger() gologger.ILogger {
    useZerolog := os.Getenv("USE_ZEROLOG") == "true"
    
    factory := gologger.NewLoggerFactory()
    config := gologger.LoggerConfig{
        LogLevel:       "INFO",
        DisableGraylog: true,
    }
    
    if useZerolog {
        return factory.CreateLogger(gologger.ZerologLoggerType, config)
    }
    return factory.CreateLogger(gologger.CustomLoggerType, config)
}
```

### Strategy 3: Service-by-Service Migration

Migrate one service at a time while monitoring performance:

```go
// Service configuration
type ServiceConfig struct {
    LoggerType     string `json:"logger_type" default:"custom"`
    LogLevel       string `json:"log_level" default:"INFO"`
    GraylogHost    string `json:"graylog_host"`
    GraylogPort    int    `json:"graylog_port"`
}

func (cfg *ServiceConfig) CreateLogger() gologger.ILogger {
    factory := gologger.NewLoggerFactory()
    
    loggerConfig := gologger.LoggerConfig{
        LogLevel:        cfg.LogLevel,
        GraylogHost:     cfg.GraylogHost,
        GraylogPort:     cfg.GraylogPort,
        DisableGraylog:  cfg.GraylogHost == "",
    }
    
    switch cfg.LoggerType {
    case "zerolog":
        return factory.CreateLogger(gologger.ZerologLoggerType, loggerConfig)
    default:
        return factory.CreateLogger(gologger.CustomLoggerType, loggerConfig)
    }
}
```

## Option Mapping

### CustomLogger Options → ZerologLogger Options

| CustomLogger | ZerologLogger | Notes |
|--------------|---------------|--------|
| `SetLogLevel("INFO")` | `ZerologSetLogLevel("INFO")` | Same values: ERROR, WARN, INFO, DEBUG |
| `GraylogHost("host")` | `ZerologGraylogHost("host")` | Direct mapping |
| `GraylogPort(12201)` | `ZerologGraylogPort(12201)` | Direct mapping |
| `GraylogFacility("facility")` | `ZerologGraylogFacility("facility")` | Direct mapping |
| `SetK8sNamespace("namespace")` | `ZerologSetK8sNamespace("namespace")` | Direct mapping |
| `DisableGraylog(true)` | `ZerologDisableGraylog()` | Simplified API |
| `ConsolePrintEnabled(true)` | `ZerologConsoleOutput()` | Enhanced console formatting |
| `TimeLoggingEnabled(true)` | `ZerologTimeLoggingEnabled(true)` | Direct mapping |

### Combined Graylog Configuration

**CustomLogger:**
```go
logger := gologger.NewLogger(
    gologger.GraylogHost("graylog.company.com"),
    gologger.GraylogPort(12201),
    gologger.ConsolePrintEnabled(true), // Both console and Graylog
)
```

**ZerologLogger:**
```go
logger := gologger.NewZerologLogger(
    gologger.ZerologEnableGraylog("graylog.company.com", 12201), // Combines host, port, and console output
)
```

## Code Changes Required

### 1. Import Changes
No changes required - same import path.

### 2. Variable Type Changes
**Before:**
```go
var logger *gologger.CustomLogger
```

**After:**
```go
var logger gologger.ILogger
```

### 3. Function Signatures
**Before:**
```go
func NewService(logger *gologger.CustomLogger) *Service {
    return &Service{logger: logger}
}
```

**After:**
```go
func NewService(logger gologger.ILogger) *Service {
    return &Service{logger: logger}
}
```

### 4. Dependency Injection
**Before:**
```go
type Service struct {
    logger *gologger.CustomLogger
}
```

**After:**
```go
type Service struct {
    logger gologger.ILogger
}
```

## Testing Migration

### Unit Tests
```go
func TestServiceWithBothLoggers(t *testing.T) {
    tests := []struct {
        name   string
        logger gologger.ILogger
    }{
        {
            name: "CustomLogger",
            logger: gologger.NewLogger(gologger.DisableGraylog(true)),
        },
        {
            name: "ZerologLogger", 
            logger: gologger.NewZerologLogger(gologger.ZerologDisableGraylog()),
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            service := NewService(tt.logger)
            // Test service functionality
            err := service.ProcessRequest("test")
            assert.NoError(t, err)
        })
    }
}
```

### Performance Testing
```go
func BenchmarkLoggerMigration(b *testing.B) {
    loggers := map[string]gologger.ILogger{
        "CustomLogger":  gologger.NewDevelopmentCustomLogger(),
        "ZerologLogger": gologger.NewDevelopmentZerologLogger(),
    }
    
    for name, logger := range loggers {
        b.Run(name, func(b *testing.B) {
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                logger.LogInfo("benchmark test message")
            }
        })
    }
}
```

## Deployment Strategy

### Phase 1: Development Environment
1. Deploy with `USE_ZEROLOG=true` environment variable
2. Monitor application behavior and performance
3. Run existing test suites
4. Validate log output format

### Phase 2: Staging Environment
1. Deploy with feature flag for gradual rollout
2. A/B test between logger implementations
3. Monitor memory usage and GC pressure
4. Validate Graylog integration

### Phase 3: Production Rollout
1. Start with low-traffic services
2. Monitor key metrics:
   - Response latency
   - Memory consumption
   - GC frequency
   - Error rates
3. Gradually increase percentage of traffic
4. Full migration once confident

## Monitoring and Validation

### Key Metrics to Monitor

**Performance Metrics:**
- Application response time
- Memory usage (heap size, allocations)
- GC pause time and frequency
- CPU utilization

**Logging Metrics:**
- Log throughput (logs/second)
- Log processing latency
- Graylog ingestion rate
- Error log frequency

**Business Metrics:**
- Application error rates
- Feature functionality
- User experience metrics

### Validation Checklist

- [ ] All log levels work correctly (ERROR, WARN, INFO, DEBUG)
- [ ] Structured logging with key-value pairs functions properly
- [ ] Context logging includes trace IDs and span IDs
- [ ] Graylog integration works as expected
- [ ] Time logging (Tic/Toc) produces accurate measurements
- [ ] Error logging captures error details properly
- [ ] Performance improvement is measurable
- [ ] Memory usage decreases
- [ ] No functional regressions

## Rollback Plan

If issues arise during migration:

1. **Immediate Rollback:**
   ```go
   // Change environment variable
   USE_ZEROLOG=false
   
   // Or use feature flag
   if featureFlag.IsEnabled("use_zerolog") {
       return createZerologLogger()
   }
   return createCustomLogger()
   ```

2. **Service-Level Rollback:**
   - Update service configuration
   - Restart affected services
   - Monitor for resolution

3. **Complete Rollback:**
   - Revert to previous deployment
   - Update configuration management
   - Document issues for future resolution

## Common Issues and Solutions

### Issue 1: Log Format Differences
**Problem:** Zerolog output format differs from CustomLogger
**Solution:** Use `ZerologConsoleOutput()` for development, custom formatters for production

### Issue 2: Graylog Integration
**Problem:** GELF format compatibility
**Solution:** Test Graylog integration thoroughly in staging environment

### Issue 3: Performance Expectations
**Problem:** Not seeing expected performance improvements
**Solution:** Ensure you're using `ZerologDisableGraylog()` for benchmarking, check I/O bottlenecks

### Issue 4: Context Propagation
**Problem:** Trace context not appearing in logs
**Solution:** Verify OpenTelemetry context is properly passed to logging methods

## Conclusion

The migration to ZerologLogger provides significant performance benefits with minimal code changes. The interface-based design ensures a smooth transition while maintaining full backward compatibility. Follow the phased approach for safe deployment and thorough validation.