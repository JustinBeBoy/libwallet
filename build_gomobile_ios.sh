#!/bin/bash
################################################################################
# Gomobile iOS Build Script for libwallet
#
# This script builds the libwallet library for iOS using gomobile, creating
# an XCFramework that can be integrated into iOS applications.
#
# Usage:
#   ./build_gomobile_ios.sh [command] [options]
#
# Commands:
#   (no args)          Build for both device and simulator (default)
#   device             Build for iOS device only (arm64)
#   simulator          Build for iOS simulator only (arm64 + x86_64)
#   all                Build for both device and simulator (explicit)
#   clean              Clean build directory
#   zip                Build and create zip archive
#   verify             Verify existing build
#   info               Show build environment information
#   help               Show this help message
#
# Options:
#   -v, --verbose      Show verbose build output
#   --no-optimize      Skip optimization flags
#   --backup           Backup existing framework before build
#   --skip-install     Skip auto-install of gomobile
#
# Examples:
#   ./build_gomobile_ios.sh                    # Build everything
#   ./build_gomobile_ios.sh device --verbose   # Build device with verbose output
#   ./build_gomobile_ios.sh zip                # Build and create zip
#
# Author: Generated for libwallet project
# Version: 1.0.0
################################################################################

set -e  # Exit on error
set -o pipefail  # Pipe failures propagate

################################################################################
# CONSTANTS & CONFIGURATION
################################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"
BUILD_DIR="$PROJECT_DIR/build"
MOBILE_DIR="$PROJECT_DIR/mobile"

FRAMEWORK_NAME="Libwallet"
XCFRAMEWORK_NAME="${FRAMEWORK_NAME}.xcframework"
MIN_GO_VERSION="1.21"
MIN_IOS_VERSION="13.0"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Flags
VERBOSE=false
OPTIMIZE=true
BACKUP=false
SKIP_INSTALL=false

# Timing
BUILD_START_TIME=0

################################################################################
# HELPER FUNCTIONS
################################################################################

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_step() {
    echo -e "${CYAN}[STEP]${NC} $1"
}

# Print banner
print_banner() {
    echo -e "${BOLD}${BLUE}"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "  Gomobile iOS Build Script - Libwallet"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo -e "${NC}"
}

# Print separator
print_separator() {
    echo -e "${BLUE}──────────────────────────────────────────────────────────────────────${NC}"
}

# Format duration (seconds to human readable)
format_duration() {
    local seconds=$1
    if [ $seconds -lt 60 ]; then
        echo "${seconds}s"
    else
        local minutes=$((seconds / 60))
        local secs=$((seconds % 60))
        echo "${minutes}m ${secs}s"
    fi
}

# Format file size
format_size() {
    local path=$1
    if [ -d "$path" ]; then
        du -sh "$path" 2>/dev/null | cut -f1 || echo "unknown"
    elif [ -f "$path" ]; then
        du -sh "$path" 2>/dev/null | cut -f1 || echo "unknown"
    else
        echo "unknown"
    fi
}

# Start timer
start_timer() {
    BUILD_START_TIME=$(date +%s)
}

# Get elapsed time
get_elapsed_time() {
    local end_time=$(date +%s)
    echo $((end_time - BUILD_START_TIME))
}

################################################################################
# VERIFICATION FUNCTIONS
################################################################################

# Check Go installation and version
check_go_version() {
    log_step "Checking Go installation..."

    if ! command -v go &> /dev/null; then
        log_error "Go is not installed"
        echo "Please install Go from https://golang.org/dl/"
        exit 1
    fi

    local go_version=$(go version | awk '{print $3}' | sed 's/go//')
    log_info "✓ Go $go_version"

    # Simple version comparison (works for most cases)
    local min_version_num=$(echo $MIN_GO_VERSION | tr -d '.')
    local current_version_num=$(echo $go_version | cut -d. -f1,2 | tr -d '.')

    if [ "$current_version_num" -lt "$min_version_num" ]; then
        log_warn "Go version $go_version is older than recommended $MIN_GO_VERSION"
    fi
}

# Check Xcode and iOS SDK
check_xcode() {
    log_step "Checking Xcode..."

    if ! command -v xcrun &> /dev/null; then
        log_error "Xcode Command Line Tools not installed"
        echo "Install with: xcode-select --install"
        exit 1
    fi

    local xcode_version=$(xcodebuild -version 2>/dev/null | head -1 || echo "Unknown")
    log_info "✓ $xcode_version"

    local ios_sdk_version=$(xcrun --sdk iphoneos --show-sdk-version 2>/dev/null || echo "Unknown")
    log_info "✓ iOS SDK $ios_sdk_version"
}

# Check gomobile installation
check_gomobile() {
    log_step "Checking gomobile..."

    if command -v gomobile &> /dev/null; then
        local gomobile_path=$(which gomobile)
        log_info "✓ gomobile installed at $gomobile_path"
        return 0
    else
        log_warn "gomobile not found"
        return 1
    fi
}

# Verify mobile package exists
verify_mobile_package() {
    log_step "Verifying mobile package..."

    if [ ! -d "$MOBILE_DIR" ]; then
        log_error "Mobile package directory not found: $MOBILE_DIR"
        exit 1
    fi

    if [ ! -f "$MOBILE_DIR/mobile.go" ]; then
        log_error "mobile.go not found in $MOBILE_DIR"
        exit 1
    fi

    log_info "✓ Mobile package found at $MOBILE_DIR"
}

# Validate XCFramework structure
validate_xcframework() {
    local xcframework_path="$BUILD_DIR/$XCFRAMEWORK_NAME"

    log_step "Validating XCFramework..."

    if [ ! -d "$xcframework_path" ]; then
        log_error "XCFramework not found at $xcframework_path"
        return 1
    fi

    # Check Info.plist
    if [ ! -f "$xcframework_path/Info.plist" ]; then
        log_error "Info.plist not found in XCFramework"
        return 1
    fi

    # Check for expected directories
    local has_device=false
    local has_simulator=false

    if [ -d "$xcframework_path/ios-arm64" ]; then
        has_device=true
        log_info "✓ Device framework found (arm64)"
    fi

    if [ -d "$xcframework_path/ios-arm64_x86_64-simulator" ]; then
        has_simulator=true
        log_info "✓ Simulator framework found (arm64 + x86_64)"
    fi

    if [ "$has_device" = false ] && [ "$has_simulator" = false ]; then
        log_error "No valid frameworks found in XCFramework"
        return 1
    fi

    local size=$(format_size "$xcframework_path")
    log_success "XCFramework validation passed ($size)"
    return 0
}

################################################################################
# INSTALLATION FUNCTIONS
################################################################################

# Install gomobile and gobind
install_gomobile() {
    if [ "$SKIP_INSTALL" = true ]; then
        log_warn "Skipping gomobile installation (--skip-install)"
        return 1
    fi

    log_step "Installing gomobile and gobind..."

    # Install gomobile
    if ! go install golang.org/x/mobile/cmd/gomobile@latest; then
        log_error "Failed to install gomobile"
        return 1
    fi

    # Install gobind
    if ! go install golang.org/x/mobile/cmd/gobind@latest; then
        log_error "Failed to install gobind"
        return 1
    fi

    log_success "gomobile and gobind installed successfully"
    return 0
}

# Initialize gomobile
init_gomobile() {
    log_step "Initializing gomobile..."

    if ! gomobile init; then
        log_error "Failed to initialize gomobile"
        return 1
    fi

    log_success "gomobile initialized"
    return 0
}

################################################################################
# BUILD FUNCTIONS
################################################################################

# Build for iOS device only
build_device() {
    log_step "Building for iOS device (arm64)..."

    local target="ios/arm64"
    local ldflags=""
    local verbose_flag=""

    if [ "$OPTIMIZE" = true ]; then
        ldflags='-ldflags="-s -w"'
    fi

    if [ "$VERBOSE" = true ]; then
        verbose_flag="-v"
    fi

    cd "$PROJECT_DIR"

    log_info "Running: gomobile bind -target=$target $ldflags $verbose_flag -o $BUILD_DIR/$XCFRAMEWORK_NAME ./mobile"

    if [ "$OPTIMIZE" = true ]; then
        if [ "$VERBOSE" = true ]; then
            gomobile bind -target="$target" -ldflags="-s -w" -v -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        else
            gomobile bind -target="$target" -ldflags="-s -w" -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        fi
    else
        if [ "$VERBOSE" = true ]; then
            gomobile bind -target="$target" -v -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        else
            gomobile bind -target="$target" -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        fi
    fi

    if [ $? -ne 0 ]; then
        log_error "Device build failed"
        return 1
    fi

    log_success "Device build completed"
    return 0
}

# Build for iOS simulator only
build_simulator() {
    log_step "Building for iOS simulator (arm64 + x86_64)..."

    local target="iossimulator"
    local ldflags=""
    local verbose_flag=""

    if [ "$OPTIMIZE" = true ]; then
        ldflags='-ldflags="-s -w"'
    fi

    if [ "$VERBOSE" = true ]; then
        verbose_flag="-v"
    fi

    cd "$PROJECT_DIR"

    log_info "Running: gomobile bind -target=$target $ldflags $verbose_flag -o $BUILD_DIR/$XCFRAMEWORK_NAME ./mobile"

    if [ "$OPTIMIZE" = true ]; then
        if [ "$VERBOSE" = true ]; then
            gomobile bind -target="$target" -ldflags="-s -w" -v -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        else
            gomobile bind -target="$target" -ldflags="-s -w" -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        fi
    else
        if [ "$VERBOSE" = true ]; then
            gomobile bind -target="$target" -v -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        else
            gomobile bind -target="$target" -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        fi
    fi

    if [ $? -ne 0 ]; then
        log_error "Simulator build failed"
        return 1
    fi

    log_success "Simulator build completed"
    return 0
}

# Build for both device and simulator
build_all() {
    log_step "Building for iOS device and simulator..."

    local target="ios"
    local ldflags=""
    local verbose_flag=""

    if [ "$OPTIMIZE" = true ]; then
        ldflags='-ldflags="-s -w"'
    fi

    if [ "$VERBOSE" = true ]; then
        verbose_flag="-v"
    fi

    cd "$PROJECT_DIR"

    log_info "Running: gomobile bind -target=$target $ldflags $verbose_flag -o $BUILD_DIR/$XCFRAMEWORK_NAME ./mobile"

    if [ "$OPTIMIZE" = true ]; then
        if [ "$VERBOSE" = true ]; then
            gomobile bind -target="$target" -ldflags="-s -w" -v -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        else
            gomobile bind -target="$target" -ldflags="-s -w" -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        fi
    else
        if [ "$VERBOSE" = true ]; then
            gomobile bind -target="$target" -v -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        else
            gomobile bind -target="$target" -o "$BUILD_DIR/$XCFRAMEWORK_NAME" ./mobile || return 1
        fi
    fi

    if [ $? -ne 0 ]; then
        log_error "Build failed"
        return 1
    fi

    log_success "Build completed"
    return 0
}

# Create zip archive
create_zip() {
    local xcframework_path="$BUILD_DIR/$XCFRAMEWORK_NAME"
    local zip_path="$BUILD_DIR/${XCFRAMEWORK_NAME}.zip"

    log_step "Creating zip archive..."

    if [ ! -d "$xcframework_path" ]; then
        log_error "XCFramework not found. Build first."
        return 1
    fi

    # Remove old zip if exists
    rm -f "$zip_path"

    # Create zip
    cd "$BUILD_DIR"
    if ! zip -r -q "${XCFRAMEWORK_NAME}.zip" "$XCFRAMEWORK_NAME"; then
        log_error "Failed to create zip archive"
        return 1
    fi

    local size=$(format_size "$zip_path")
    log_success "Zip archive created: ${XCFRAMEWORK_NAME}.zip ($size)"
    return 0
}

################################################################################
# UTILITY FUNCTIONS
################################################################################

# Clean build directory
clean_build() {
    log_step "Cleaning build directory..."

    if [ -d "$BUILD_DIR" ]; then
        rm -rf "$BUILD_DIR"
        log_success "Build directory cleaned"
    else
        log_info "Build directory already clean"
    fi
}

# Backup existing framework
backup_existing() {
    local xcframework_path="$BUILD_DIR/$XCFRAMEWORK_NAME"

    if [ ! -d "$xcframework_path" ]; then
        return 0
    fi

    local timestamp=$(date +%Y%m%d_%H%M%S)
    local backup_path="${xcframework_path}_backup_${timestamp}"

    log_step "Backing up existing framework..."
    cp -R "$xcframework_path" "$backup_path"
    log_success "Backup created: $(basename $backup_path)"
}

# Generate build info JSON
generate_build_info() {
    local xcframework_path="$BUILD_DIR/$XCFRAMEWORK_NAME"
    local info_path="$BUILD_DIR/build-info.json"

    if [ ! -d "$xcframework_path" ]; then
        return 0
    fi

    log_step "Generating build info..."

    local build_date=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
    local elapsed=$(get_elapsed_time)
    local go_version=$(go version | awk '{print $3}' | sed 's/go//')
    local xcode_version=$(xcodebuild -version 2>/dev/null | head -1 | awk '{print $2}' || echo "Unknown")
    local ios_sdk=$(xcrun --sdk iphoneos --show-sdk-version 2>/dev/null || echo "Unknown")
    local size=$(format_size "$xcframework_path")
    local git_commit=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
    local git_branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")

    cat > "$info_path" << EOF
{
  "framework_name": "$XCFRAMEWORK_NAME",
  "build_date": "$build_date",
  "build_duration": "${elapsed}s",
  "go_version": "$go_version",
  "xcode_version": "$xcode_version",
  "ios_sdk_version": "$ios_sdk",
  "gomobile_version": "latest",
  "target": "ios",
  "optimizations": $OPTIMIZE,
  "framework_size": "$size",
  "git_commit": "$git_commit",
  "git_branch": "$git_branch"
}
EOF

    log_success "Build info generated: build-info.json"
}

# Show build environment info
show_info() {
    print_banner
    echo "Build Environment Information:"
    print_separator

    # Go
    if command -v go &> /dev/null; then
        echo -e "${GREEN}Go:${NC} $(go version)"
    else
        echo -e "${RED}Go:${NC} Not installed"
    fi

    # Xcode
    if command -v xcodebuild &> /dev/null; then
        echo -e "${GREEN}Xcode:${NC} $(xcodebuild -version 2>/dev/null | head -1 || echo 'Unknown')"
        local ios_sdk=$(xcrun --sdk iphoneos --show-sdk-version 2>/dev/null || echo "Unknown")
        echo -e "${GREEN}iOS SDK:${NC} $ios_sdk"
    else
        echo -e "${RED}Xcode:${NC} Not installed"
    fi

    # gomobile
    if command -v gomobile &> /dev/null; then
        echo -e "${GREEN}gomobile:${NC} $(which gomobile)"
    else
        echo -e "${YELLOW}gomobile:${NC} Not installed"
    fi

    # Paths
    print_separator
    echo "Project Paths:"
    echo -e "${CYAN}Project Dir:${NC} $PROJECT_DIR"
    echo -e "${CYAN}Build Dir:${NC} $BUILD_DIR"
    echo -e "${CYAN}Mobile Dir:${NC} $MOBILE_DIR"

    # Build artifacts
    if [ -d "$BUILD_DIR/$XCFRAMEWORK_NAME" ]; then
        print_separator
        echo "Build Artifacts:"
        local size=$(format_size "$BUILD_DIR/$XCFRAMEWORK_NAME")
        echo -e "${CYAN}XCFramework:${NC} $XCFRAMEWORK_NAME ($size)"

        if [ -f "$BUILD_DIR/${XCFRAMEWORK_NAME}.zip" ]; then
            local zip_size=$(format_size "$BUILD_DIR/${XCFRAMEWORK_NAME}.zip")
            echo -e "${CYAN}Zip Archive:${NC} ${XCFRAMEWORK_NAME}.zip ($zip_size)"
        fi
    fi

    print_separator
}

# Show help
show_help() {
    cat << 'EOF'
Gomobile iOS Build Script for libwallet

Usage: ./build_gomobile_ios.sh [command] [options]

Commands:
  (no args)          Build for both device and simulator (default)
  device             Build for iOS device only (arm64)
  simulator          Build for iOS simulator only (arm64 + x86_64)
  all                Build for both device and simulator (explicit)
  clean              Clean build directory
  zip                Build and create zip archive
  verify             Verify existing build
  info               Show build environment information
  help               Show this help message

Options:
  -v, --verbose      Show verbose build output
  --no-optimize      Skip optimization flags (-ldflags="-s -w")
  --backup           Backup existing framework before build
  --skip-install     Skip auto-install of gomobile

Examples:
  ./build_gomobile_ios.sh                    # Build everything
  ./build_gomobile_ios.sh device --verbose   # Build device with verbose output
  ./build_gomobile_ios.sh zip                # Build and create zip
  ./build_gomobile_ios.sh clean              # Clean build directory
  ./build_gomobile_ios.sh info               # Show environment info

Output:
  build/Libwallet.xcframework/               # XCFramework output
  build/Libwallet.xcframework.zip            # Zip archive (with 'zip' command)
  build/build-info.json                      # Build metadata

For more information, see jt-docs/BUILD_IOS_GOMOBILE.md
EOF
}

################################################################################
# MAIN SCRIPT
################################################################################

# Parse command line arguments
COMMAND="${1:-all}"
shift || true

while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        --no-optimize)
            OPTIMIZE=false
            shift
            ;;
        --backup)
            BACKUP=true
            shift
            ;;
        --skip-install)
            SKIP_INSTALL=true
            shift
            ;;
        *)
            log_error "Unknown option: $1"
            echo "Run './build_gomobile_ios.sh help' for usage information"
            exit 1
            ;;
    esac
done

# Main execution
main() {
    # Handle help and info commands without banner
    if [ "$COMMAND" = "help" ] || [ "$COMMAND" = "--help" ] || [ "$COMMAND" = "-h" ]; then
        show_help
        exit 0
    fi

    if [ "$COMMAND" = "info" ]; then
        show_info
        exit 0
    fi

    # Print banner
    print_banner

    # Handle clean command
    if [ "$COMMAND" = "clean" ]; then
        clean_build
        exit 0
    fi

    # Handle verify command
    if [ "$COMMAND" = "verify" ]; then
        if validate_xcframework; then
            exit 0
        else
            exit 1
        fi
    fi

    # Start timer
    start_timer

    # Run verifications
    echo "Build Configuration:"
    echo -e "${CYAN}Target:${NC} $COMMAND"
    echo -e "${CYAN}Optimize:${NC} $OPTIMIZE"
    echo -e "${CYAN}Verbose:${NC} $VERBOSE"
    echo -e "${CYAN}Backup:${NC} $BACKUP"
    print_separator

    check_go_version
    check_xcode
    verify_mobile_package

    # Check and install gomobile if needed
    if ! check_gomobile; then
        if install_gomobile; then
            init_gomobile
        else
            log_error "gomobile installation failed and --skip-install was set"
            exit 1
        fi
    fi

    print_separator

    # Create build directory
    mkdir -p "$BUILD_DIR"

    # Backup if requested
    if [ "$BACKUP" = true ]; then
        backup_existing
    fi

    # Execute build command
    local build_success=false

    case "$COMMAND" in
        device)
            if build_device; then
                build_success=true
            fi
            ;;
        simulator)
            if build_simulator; then
                build_success=true
            fi
            ;;
        all|"")
            if build_all; then
                build_success=true
            fi
            ;;
        zip)
            if build_all; then
                if create_zip; then
                    build_success=true
                fi
            fi
            ;;
        *)
            log_error "Unknown command: $COMMAND"
            echo "Run './build_gomobile_ios.sh help' for usage information"
            exit 1
            ;;
    esac

    if [ "$build_success" = false ]; then
        log_error "Build failed"
        exit 1
    fi

    print_separator

    # Validate build
    validate_xcframework

    # Generate build info
    generate_build_info

    # Print summary
    print_separator
    local elapsed=$(get_elapsed_time)
    local duration=$(format_duration $elapsed)
    local size=$(format_size "$BUILD_DIR/$XCFRAMEWORK_NAME")

    echo ""
    log_success "Build completed successfully in $duration"
    echo ""
    echo "Output:"
    echo -e "  ${CYAN}Framework:${NC} $BUILD_DIR/$XCFRAMEWORK_NAME ($size)"

    if [ -f "$BUILD_DIR/${XCFRAMEWORK_NAME}.zip" ]; then
        local zip_size=$(format_size "$BUILD_DIR/${XCFRAMEWORK_NAME}.zip")
        echo -e "  ${CYAN}Zip:${NC} $BUILD_DIR/${XCFRAMEWORK_NAME}.zip ($zip_size)"
    fi

    if [ -f "$BUILD_DIR/build-info.json" ]; then
        echo -e "  ${CYAN}Build Info:${NC} $BUILD_DIR/build-info.json"
    fi

    echo ""
    print_separator
}

# Run main function
main
