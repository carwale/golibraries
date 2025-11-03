# Logger Benchmark Runner Script (PowerShell)
# This script runs comprehensive benchmarks comparing CustomLogger vs Zerolog

Write-Host "==================================" -ForegroundColor Cyan
Write-Host "Logger Performance Benchmark Suite" -ForegroundColor Cyan
Write-Host "==================================" -ForegroundColor Cyan
Write-Host "Comparing CustomLogger vs Zerolog" -ForegroundColor Yellow
Write-Host ""

# Create output directory
$benchmarkDir = "benchmark_results"
if (!(Test-Path $benchmarkDir)) {
    New-Item -ItemType Directory -Path $benchmarkDir | Out-Null
}

Set-Location $benchmarkDir

# Run benchmarks and save results
Write-Host "Running benchmarks..." -ForegroundColor Green
Set-Location gologger
$benchmarkCommand = "go test -bench='BenchmarkComparison_|BenchmarkCustomLogger_Info|BenchmarkZerolog_Info_Discard|BenchmarkCustomLogger_Allocations|BenchmarkZerolog_Allocations' -benchmem -count=3 ."
Invoke-Expression "$benchmarkCommand 2>null" > ../benchmark_results_raw.txt
Set-Location ..

# Check if benchmark ran successfully
if ($LASTEXITCODE -ne 0) {
    Write-Host "Benchmark execution failed. Check benchmark_results_raw.txt for details." -ForegroundColor Red
    Get-Content benchmark_results_raw.txt | Select-Object -Last 20
    exit 1
}

Write-Host "Processing results..." -ForegroundColor Green

# Extract benchmark results
Get-Content benchmark_results_raw.txt | Select-String "Benchmark.*ns/op|Benchmark.*B/op|Benchmark.*allocs/op" > parsed_results.txt

Write-Host ""
Write-Host "Benchmark Results Summary" -ForegroundColor Cyan
Write-Host "========================" -ForegroundColor Cyan
Write-Host ""

# Display results in a readable format
Get-Content parsed_results.txt | ForEach-Object {
    if ($_ -like "*CustomLogger*") {
        Write-Host "🔵 $_" -ForegroundColor Blue
    } elseif ($_ -like "*Zerolog*") {
        Write-Host "🟢 $_" -ForegroundColor Green
    } else {
        Write-Host "   $_"
    }
}

Write-Host ""
Write-Host "Full results saved to: benchmark_results/benchmark_results_raw.txt" -ForegroundColor Yellow
Write-Host "Parsed results saved to: benchmark_results/parsed_results.txt" -ForegroundColor Yellow
Write-Host ""
Write-Host "Key Metrics:" -ForegroundColor Cyan
Write-Host "- ns/op: Nanoseconds per operation (lower is better)" -ForegroundColor White
Write-Host "- B/op: Bytes allocated per operation (lower is better)" -ForegroundColor White
Write-Host "- allocs/op: Number of allocations per operation (lower is better)" -ForegroundColor White

# Generate summary report
Write-Host ""
Write-Host "Generating Performance Summary Report..." -ForegroundColor Green

$summaryReport = @"
Logger Performance Benchmark Summary Report
==========================================
Generated on: $(Get-Date)

Test Environment:
- OS: $((Get-WmiObject Win32_OperatingSystem).Caption)
- Go Version: $(go version)
- CPU: $((Get-WmiObject Win32_Processor).Name)

Benchmark Categories:
1. Basic Logging (Info, Error, Debug)
2. Formatted Logging (with parameters)
3. Structured Logging (with fields)
4. Context Logging (with tracing)
5. Level Filtering Performance
6. Memory Allocation Analysis
7. I/O Impact Analysis

Key Findings:
$(Get-Content parsed_results.txt | Out-String)

Performance Analysis:
- Zerolog is generally expected to be faster due to zero-allocation design
- CustomLogger provides more built-in features like Graylog integration
- Memory allocation patterns differ significantly between implementations
- Context propagation overhead varies between loggers

Recommendations:
- For high-performance applications: Consider Zerolog
- For feature-rich logging with Graylog: Current CustomLogger
- For mixed scenarios: Evaluate based on specific use case requirements

Full benchmark data available in: benchmark_results_raw.txt
"@

$summaryReport | Out-File -FilePath "performance_summary.txt" -Encoding UTF8

Write-Host "Performance summary report saved to: benchmark_results/performance_summary.txt" -ForegroundColor Green

# Return to original directory
Set-Location ..