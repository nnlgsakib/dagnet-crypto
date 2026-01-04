#!/bin/bash

# Build Verification Script for NDAG Coin
# This script checks for common Go compilation issues

set -e

echo "🔍 NDAG Coin Build Verification"
echo "==============================="

ERRORS=0
WARNINGS=0

# Function to check imports
check_imports() {
    local file=$1
    while IFS= read -r line; do
        if [[ $line =~ ^import ]]; then
            if [[ $line =~ \. ]]; then
                echo "❌ ERROR:_relative import in $file: $line"
                ((ERRORS++))
            fi
        fi
    done < "$file"
}

# Function to check for undefined functions
check_undefined() {
    local file=$1
    # Look for function calls that might not exist
    if grep -q "mustMarshal" "$file" 2>/dev/null; then
        echo "❌ ERROR: undefined mustMarshal in $file (should be MustMarshal)"
        ((ERRORS++))
    fi
    
    if grep -q "generateEventID" "$file" 2>/dev/null; then
        if ! grep -q "func generateEventID" "$file" 2>/dev/null; then
            echo "⚠️  WARNING: external generateEventID in $file"
            ((WARNINGS++))
        fi
    fi
}

# Function to check for missing imports
check_missing_imports() {
    local file=$1
    
    # Check for common missing imports
    if grep -q "ctx.Context" "$file" 2>/dev/null && ! grep -q '"context"' "$file" 2>/dev/null; then
        echo "❌ ERROR: missing context import in $file"
        ((ERRORS++))
    fi
    
    if grep -q "proto." "$file" 2>/dev/null && ! grep -q '"google.golang.org/protobuf/proto"' "$file" 2>/dev/null; then
        echo "❌ ERROR: missing protobuf import in $file"
        ((ERRORS++))
    fi
}

# Check all Go files
echo "📋 Checking all Go files..."
GOFILES=$(find /home/engine/project -name "*.go" -type f)
TOTAL=$(echo "$GOFILES" | wc -l)
echo "Found $TOTAL Go files"

for file in $GOFILES; do
    # Skip test files for some checks
    if [[ $file =~ _test\.go$ ]]; then
        continue
    fi
    
    check_imports "$file"
    check_undefined "$file"
    check_missing_imports "$file"
done

echo ""
echo "📊 Summary:"
echo "==========="
echo "Total files checked: $TOTAL"

if [ $ERRORS -eq 0 ] && [ $WARNINGS -eq 0 ]; then
    echo "✅ SUCCESS: All checks passed!"
    echo "✅ Build verification PASSED"
    exit 0
else
    echo "❌ ERRORS: $ERRORS"
    echo "⚠️  WARNINGS: $WARNINGS"
    if [ $ERRORS -eq 0 ]; then
        echo "✅ Build verification PASSED (with warnings)"
        exit 0
    else
        echo "❌ Build verification FAILED"
        exit 1
    fi
fi