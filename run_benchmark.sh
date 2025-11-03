#!/usr/bin/env bash

# Logger Benchmark Runner Script
# This script runs comprehensive benchmarks comparing CustomLogger vs Zerolog

echo "=================================="
echo "Logger Performance Benchmark Suite"
echo "=================================="
echo "Comparing CustomLogger vs Zerolog"
echo ""

# Create output directory
mkdir -p benchmark_results
cd benchmark_results

# Run benchmarks and save results
echo "Running benchmarks..."
go test -bench=. -benchmem -count=3 -timeout=30m ../gologger/logger_benchmark_test.go ../gologger/LogManager.go ../gologger/LogLevels.go > benchmark_results_raw.txt 2>&1

# Parse and format results
echo "Processing results..."

# Extract benchmark results
grep "Benchmark" benchmark_results_raw.txt | grep -E "(ns/op|allocs/op|B/op)" > parsed_results.txt

echo ""
echo "Benchmark Results Summary"
echo "========================"
echo ""

# Display results in a readable format
while IFS= read -r line; do
    if [[ $line == *"CustomLogger"* ]]; then
        echo "🔵 $line"
    elif [[ $line == *"Zerolog"* ]]; then
        echo "🟢 $line"
    else
        echo "   $line"
    fi
done < parsed_results.txt

echo ""
echo "Full results saved to: benchmark_results/benchmark_results_raw.txt"
echo "Parsed results saved to: benchmark_results/parsed_results.txt"
echo ""
echo "Key Metrics:"
echo "- ns/op: Nanoseconds per operation (lower is better)"
echo "- B/op: Bytes allocated per operation (lower is better)" 
echo "- allocs/op: Number of allocations per operation (lower is better)"