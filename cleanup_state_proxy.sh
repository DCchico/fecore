#!/bin/bash

# State Proxy Cleanup Script
# This script removes stale semaphores and shared memory files

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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
    echo "  -a, --all           Clean up all test-related files (default)"
    echo "  -s, --semaphores    Clean up only semaphores"
    echo "  -m, --memory        Clean up only shared memory"
    echo "  -p, --pattern PAT   Clean up files matching pattern (e.g., 'vt_*')"
    echo "  -h, --help          Show this help message"
    echo ""
    echo "Examples:"
    echo "  $0                  # Clean up all test files"
    echo "  $0 -s               # Clean up only semaphores"
    echo "  $0 -p 'vt_*'        # Clean up files starting with 'vt_'"
}

# Parse command line arguments
CLEAN_SEMAPHORES=true
CLEAN_MEMORY=true
PATTERN=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -a|--all)
            CLEAN_SEMAPHORES=true
            CLEAN_MEMORY=true
            shift
            ;;
        -s|--semaphores)
            CLEAN_SEMAPHORES=true
            CLEAN_MEMORY=false
            shift
            ;;
        -m|--memory)
            CLEAN_SEMAPHORES=false
            CLEAN_MEMORY=true
            shift
            ;;
        -p|--pattern)
            PATTERN="$2"
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

# Function to clean up semaphores
cleanup_semaphores() {
    print_status "Cleaning up semaphores..."
    
    local count=0
    if [[ -n "$PATTERN" ]]; then
        # Clean up semaphores matching pattern
        for sem in /dev/shm/*_sem; do
            if [[ -f "$sem" ]] && [[ "$(basename "$sem")" == $PATTERN ]]; then
                print_status "Removing semaphore: $sem"
                rm -f "$sem"
                ((count++))
            fi
        done
    else
        # Clean up all test-related semaphores
        for sem in /dev/shm/*_sem; do
            if [[ -f "$sem" ]]; then
                print_status "Removing semaphore: $sem"
                rm -f "$sem"
                ((count++))
            fi
        done
    fi
    
    print_success "Removed $count semaphores"
}

# Function to clean up shared memory
cleanup_memory() {
    print_status "Cleaning up shared memory..."
    
    local count=0
    if [[ -n "$PATTERN" ]]; then
        # Clean up shared memory matching pattern
        for shm in /dev/shm/$PATTERN; do
            if [[ -f "$shm" ]] && [[ "$(basename "$shm")" != *"_sem" ]]; then
                print_status "Removing shared memory: $shm"
                rm -f "$shm"
                ((count++))
            fi
        done
    else
        # Clean up all test-related shared memory (excluding semaphores)
        for shm in /dev/shm/vt_* /dev/shm/test_*; do
            if [[ -f "$shm" ]] && [[ "$(basename "$shm")" != *"_sem" ]]; then
                print_status "Removing shared memory: $shm"
                rm -f "$shm"
                ((count++))
            fi
        done
    fi
    
    print_success "Removed $count shared memory files"
}

# Function to list current state
list_current_state() {
    print_status "Current state of /dev/shm:"
    
    local sem_count=0
    local shm_count=0
    
    echo "Semaphores:"
    for sem in /dev/shm/*_sem; do
        if [[ -f "$sem" ]]; then
            echo "  - $(basename "$sem")"
            ((sem_count++))
        fi
    done
    
    if [[ $sem_count -eq 0 ]]; then
        echo "  (none)"
    fi
    
    echo "Shared Memory:"
    for shm in /dev/shm/vt_* /dev/shm/test_*; do
        if [[ -f "$shm" ]] && [[ "$(basename "$shm")" != *"_sem" ]]; then
            echo "  - $(basename "$shm")"
            ((shm_count++))
        fi
    done
    
    if [[ $shm_count -eq 0 ]]; then
        echo "  (none)"
    fi
    
    echo ""
}

# Main execution
main() {
    echo "=========================================="
    echo "    State Proxy Cleanup Script"
    echo "=========================================="
    echo ""
    
    # Check if running as root (needed for /dev/shm access)
    if [[ $EUID -ne 0 ]]; then
        print_warning "Not running as root. Some files might not be accessible."
    fi
    
    # Show current state
    list_current_state
    
    # Ask for confirmation
    if [[ -n "$PATTERN" ]]; then
        print_warning "Will clean up files matching pattern: $PATTERN"
    else
        print_warning "Will clean up all test-related files"
    fi
    
    read -p "Continue? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        print_status "Cleanup cancelled"
        exit 0
    fi
    
    # Perform cleanup
    if [[ "$CLEAN_SEMAPHORES" == true ]]; then
        cleanup_semaphores
    fi
    
    if [[ "$CLEAN_MEMORY" == true ]]; then
        cleanup_memory
    fi
    
    echo ""
    print_status "Cleanup completed. Current state:"
    list_current_state
    
    echo "=========================================="
    echo "    Cleanup Complete"
    echo "=========================================="
}

# Run main function
main "$@" 