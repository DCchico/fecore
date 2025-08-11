#!/bin/bash

# State Proxy Testing Script
# This script runs the state proxy tests with different configurations

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
STATE_PROXY_URL="http://localhost:8081"
TEST_TYPE="all"

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to show usage
show_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -u, --url URL        State proxy URL (default: http://localhost:8081)"
    echo "  -t, --test TYPE      Test type: all, basic, frame0, sequence, concurrent, errors"
    echo "  -h, --help           Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                                    # Run all tests"
    echo "  $0 -u http://192.168.1.100:8081      # Test remote state proxy"
    echo "  $0 -t basic                          # Run only basic accessibility test"
    echo "  $0 -t sequence                       # Run only frame sequence test"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -u|--url)
            STATE_PROXY_URL="$2"
            shift 2
            ;;
        -t|--test)
            TEST_TYPE="$2"
            shift 2
            ;;
        -h|--help)
            show_usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            show_usage
            exit 1
            ;;
    esac
done

# Validate test type
valid_tests=("all" "basic" "frame0" "sequence" "concurrent" "errors")
if [[ ! " ${valid_tests[@]} " =~ " ${TEST_TYPE} " ]]; then
    print_error "Invalid test type: $TEST_TYPE"
    print_error "Valid test types: ${valid_tests[*]}"
    exit 1
fi

# Check if Python and required packages are available
check_dependencies() {
    print_status "Checking dependencies..."
    
    if ! command -v python3 &> /dev/null; then
        print_error "Python 3 is not installed"
        exit 1
    fi
    
    if ! python3 -c "import requests" &> /dev/null; then
        print_error "Python requests module is not installed"
        print_status "Install it with: pip3 install requests"
        exit 1
    fi
    
    print_success "Dependencies check passed"
}

# Check if state proxy is accessible
check_state_proxy() {
    print_status "Checking state proxy accessibility..."
    
    if curl -s --connect-timeout 5 "$STATE_PROXY_URL/state" > /dev/null 2>&1; then
        print_success "State proxy is accessible at $STATE_PROXY_URL"
    else
        print_warning "State proxy might not be accessible at $STATE_PROXY_URL"
        print_status "Make sure the state proxy is running and accessible"
        read -p "Continue anyway? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 1
        fi
    fi
}

# Run the tests
run_tests() {
    print_status "Starting state proxy tests..."
    print_status "URL: $STATE_PROXY_URL"
    print_status "Test type: $TEST_TYPE"
    echo ""
    
    # Make the test script executable
    chmod +x test_state_proxy.py
    
    # Run the tests
    if python3 test_state_proxy.py --url "$STATE_PROXY_URL" --test "$TEST_TYPE"; then
        print_success "All tests passed!"
        return 0
    else
        print_error "Some tests failed!"
        return 1
    fi
}

# Main execution
main() {
    echo "=========================================="
    echo "    State Proxy Testing Suite"
    echo "=========================================="
    echo ""
    
    check_dependencies
    check_state_proxy
    run_tests
    
    echo ""
    echo "=========================================="
    echo "    Testing Complete"
    echo "=========================================="
}

# Run main function
main "$@" 