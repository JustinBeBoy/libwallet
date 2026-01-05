#!/bin/bash
################################################################################
# Gomobile Android Build Script for libwallet
#
# Builds the libwallet library for Android using gomobile, producing an AAR
# that can be integrated into Android applications.
#
# Usage:
#   ./build_gomobile_android.sh [command] [options]
#
# Commands:
#   (no args)          Build AAR (default)
#   aar                Build AAR (explicit)
#   clean              Clean build directory
#   zip                Build and create zip archive
#   verify             Verify existing AAR
#   info               Show build environment information
#   help               Show this help message
#
# Options:
#   -v, --verbose      Show verbose build output
#   --no-optimize      Skip optimization flags
#   --backup           Backup existing AAR before build
#   --skip-install     Skip auto-install of gomobile
#   --min-sdk <num>    Override Android minSdkVersion (default 21)
#
# Output:
#   build/libwallet.aar
#
################################################################################

set -e
set -o pipefail

################################################################################
# CONSTANTS & CONFIGURATION
################################################################################

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$SCRIPT_DIR"
BUILD_DIR="$PROJECT_DIR/build"
GOMOBILE_DIR="$PROJECT_DIR/gomobile"

AAR_NAME="libwallet.aar"
MIN_GO_VERSION="1.21"
DEFAULT_MIN_SDK="21"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# Flags
VERBOSE=false
OPTIMIZE=true
BACKUP=false
SKIP_INSTALL=false
MIN_SDK="$DEFAULT_MIN_SDK"

BUILD_START_TIME=0

################################################################################
# HELPER FUNCTIONS
################################################################################

log_info()    { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn()    { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1"; }
log_success() { echo -e "${GREEN}[SUCCESS]${NC} $1"; }
log_step()    { echo -e "${CYAN}[STEP]${NC} $1"; }

print_banner() {
  echo -e "${BOLD}${BLUE}"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "  Gomobile Android Build Script - libwallet"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo -e "${NC}"
}

print_separator() {
  echo -e "${BLUE}──────────────────────────────────────────────────────────────────────${NC}"
}

start_timer() { BUILD_START_TIME=$(date +%s); }
get_elapsed_time() { echo $(( $(date +%s) - BUILD_START_TIME )); }

format_duration() {
  local s=$1
  if [ $s -lt 60 ]; then echo "${s}s"; else echo "$((s/60))m $((s%60))s"; fi
}

format_size() {
  local p=$1
  if [ -e "$p" ]; then du -sh "$p" 2>/dev/null | cut -f1 || echo "unknown"; else echo "unknown"; fi
}

################################################################################
# VERIFICATION
################################################################################

check_go_version() {
  log_step "Checking Go installation..."
  command -v go >/dev/null || { log_error "Go not installed"; exit 1; }

  local gv
  gv=$(go version | awk '{print $3}' | sed 's/go//')
  log_info "✓ Go $gv"
}

check_gomobile() {
  log_step "Checking gomobile..."
  if command -v gomobile >/dev/null; then
    log_info "✓ gomobile installed at $(which gomobile)"
    return 0
  fi
  log_warn "gomobile not found"
  return 1
}

install_gomobile() {
  if [ "$SKIP_INSTALL" = true ]; then
    log_warn "Skipping gomobile installation (--skip-install)"
    return 1
  fi

  log_step "Installing gomobile + gobind..."
  go install golang.org/x/mobile/cmd/gomobile@latest
  go install golang.org/x/mobile/cmd/gobind@latest
  log_success "gomobile installed"
  return 0
}

init_gomobile() {
  log_step "Initializing gomobile..."
  gomobile init
  log_success "gomobile initialized"
}

verify_mobile_package() {
  log_step "Verifying libwallet package..."
  [ -d "$GOMOBILE_DIR" ] || { log_error "Missing: $GOMOBILE_DIR"; exit 1; }
  [ -f "$GOMOBILE_DIR/gomobile.go" ] || { log_error "Missing: $GOMOBILE_DIR/gomobile.go"; exit 1; }
  log_info "✓ Libwallet package found at $GOMOBILE_DIR"
}

check_android_env() {
  log_step "Checking Android SDK/NDK environment..."

  # ANDROID_HOME / ANDROID_SDK_ROOT
  if [ -z "${ANDROID_HOME:-}" ] && [ -z "${ANDROID_SDK_ROOT:-}" ]; then
    log_warn "ANDROID_HOME/ANDROID_SDK_ROOT not set."
    log_warn "Set one of them, e.g.: export ANDROID_SDK_ROOT=\$HOME/Android/Sdk"
  else
    log_info "✓ Android SDK: ${ANDROID_SDK_ROOT:-$ANDROID_HOME}"
  fi

  # NDK
  if [ -z "${ANDROID_NDK_HOME:-}" ]; then
    log_warn "ANDROID_NDK_HOME not set."
    log_warn "gomobile needs an NDK. Set it, e.g.: export ANDROID_NDK_HOME=\$HOME/Android/Sdk/ndk/<version>"
  else
    log_info "✓ Android NDK: $ANDROID_NDK_HOME"
  fi

  # We won't hard-fail here because some setups still work via local.properties,
  # but if build fails, these are the first things to fix.
}

################################################################################
# BUILD
################################################################################

backup_existing() {
  local aar_path="$BUILD_DIR/$AAR_NAME"
  [ -f "$aar_path" ] || return 0
  local ts
  ts=$(date +%Y%m%d_%H%M%S)
  local backup="$BUILD_DIR/${AAR_NAME}.backup_${ts}"
  log_step "Backing up existing AAR..."
  cp "$aar_path" "$backup"
  log_success "Backup created: $(basename "$backup")"
}

build_aar() {
  log_step "Building Android AAR (minSdk=$MIN_SDK)..."
  mkdir -p "$BUILD_DIR"

  local verbose_flag=""
  [ "$VERBOSE" = true ] && verbose_flag="-v"

  # Optimization flags
  local ldflags=()
  if [ "$OPTIMIZE" = true ]; then
    ldflags=(-ldflags "-s -w")
  fi

  # gomobile reads minSdk from env var
  export ANDROID_API="$MIN_SDK"

  cd "$PROJECT_DIR"

  log_info "Running: gomobile bind -target=android -androidapi=$MIN_SDK ${ldflags[*]} $verbose_flag -o $BUILD_DIR/$AAR_NAME ./gomobile"

  if [ "$VERBOSE" = true ]; then
    gomobile bind -target=android -androidapi="$MIN_SDK" "${ldflags[@]}" -v -o "$BUILD_DIR/$AAR_NAME" ./gomobile
  else
    gomobile bind -target=android -androidapi="$MIN_SDK" "${ldflags[@]}" -o "$BUILD_DIR/$AAR_NAME" ./gomobile
  fi

  log_success "AAR build completed"
}

validate_aar() {
  local aar_path="$BUILD_DIR/$AAR_NAME"
  log_step "Validating AAR..."

  [ -f "$aar_path" ] || { log_error "AAR not found: $aar_path"; return 1; }

  # Quick sanity check: must contain classes.jar + AndroidManifest.xml
  if unzip -l "$aar_path" | grep -q "classes.jar" && unzip -l "$aar_path" | grep -q "AndroidManifest.xml"; then
    local size
    size=$(format_size "$aar_path")
    log_success "AAR validation passed ($size)"
    return 0
  fi

  log_error "AAR structure doesn't look right (missing classes.jar or AndroidManifest.xml)"
  return 1
}

create_zip() {
  local aar_path="$BUILD_DIR/$AAR_NAME"
  local zip_path="$BUILD_DIR/${AAR_NAME}.zip"

  log_step "Creating zip archive..."
  [ -f "$aar_path" ] || { log_error "AAR not found. Build first."; return 1; }

  rm -f "$zip_path"
  (cd "$BUILD_DIR" && zip -q -r "$(basename "$zip_path")" "$(basename "$aar_path")")
  log_success "Zip created: $(basename "$zip_path") ($(format_size "$zip_path"))"
}

clean_build() {
  log_step "Cleaning build directory..."
  rm -rf "$BUILD_DIR"
  log_success "Build directory cleaned"
}

show_info() {
  print_banner
  echo "Build Environment Information:"
  print_separator

  if command -v go >/dev/null; then echo -e "${GREEN}Go:${NC} $(go version)"; else echo -e "${RED}Go:${NC} Not installed"; fi
  if command -v gomobile >/dev/null; then echo -e "${GREEN}gomobile:${NC} $(which gomobile)"; else echo -e "${YELLOW}gomobile:${NC} Not installed"; fi

  echo -e "${GREEN}ANDROID_SDK_ROOT:${NC} ${ANDROID_SDK_ROOT:-"(not set)"}"
  echo -e "${GREEN}ANDROID_HOME:${NC} ${ANDROID_HOME:-"(not set)"}"
  echo -e "${GREEN}ANDROID_NDK_HOME:${NC} ${ANDROID_NDK_HOME:-"(not set)"}"
  echo -e "${GREEN}ANDROID_API (minSdk):${NC} $MIN_SDK"

  print_separator
  echo -e "${CYAN}Project Dir:${NC} $PROJECT_DIR"
  echo -e "${CYAN}Build Dir:${NC} $BUILD_DIR"
  echo -e "${CYAN}Gomobile Dir:${NC} $GOMOBILE_DIR"

  if [ -f "$BUILD_DIR/$AAR_NAME" ]; then
    print_separator
    echo "Build Artifacts:"
    echo -e "${CYAN}AAR:${NC} $BUILD_DIR/$AAR_NAME ($(format_size "$BUILD_DIR/$AAR_NAME"))"
  fi

  print_separator
}

show_help() {
cat << 'EOF'
Gomobile Android Build Script for libwallet

Usage: ./build_gomobile_android.sh [command] [options]

Commands:
  (no args)          Build AAR (default)
  aar                Build AAR
  clean              Clean build directory
  zip                Build and create zip archive
  verify             Verify existing build
  info               Show build environment information
  help               Show this help message

Options:
  -v, --verbose      Show verbose build output
  --no-optimize      Skip optimization flags (-ldflags "-s -w")
  --backup           Backup existing AAR before build
  --skip-install     Skip auto-install of gomobile
  --min-sdk <num>    Override minSdkVersion (default 21)

Examples:
  ./build_gomobile_android.sh
  ./build_gomobile_android.sh aar --min-sdk 23
  ./build_gomobile_android.sh zip
  ./build_gomobile_android.sh verify
EOF
}

################################################################################
# MAIN
################################################################################

COMMAND="${1:-aar}"
shift || true

while [[ $# -gt 0 ]]; do
  case $1 in
    -v|--verbose) VERBOSE=true; shift ;;
    --no-optimize) OPTIMIZE=false; shift ;;
    --backup) BACKUP=true; shift ;;
    --skip-install) SKIP_INSTALL=true; shift ;;
    --min-sdk) MIN_SDK="${2:-$DEFAULT_MIN_SDK}"; shift 2 ;;
    help|--help|-h) show_help; exit 0 ;;
    *) log_error "Unknown option: $1"; echo "Run './build_gomobile_android.sh help'"; exit 1 ;;
  esac
done

main() {
  if [ "$COMMAND" = "info" ]; then show_info; exit 0; fi
  if [ "$COMMAND" = "clean" ]; then print_banner; clean_build; exit 0; fi
  if [ "$COMMAND" = "verify" ]; then print_banner; validate_aar; exit $?; fi

  print_banner
  start_timer

  echo "Build Configuration:"
  echo -e "${CYAN}Command:${NC} $COMMAND"
  echo -e "${CYAN}Min SDK:${NC} $MIN_SDK"
  echo -e "${CYAN}Optimize:${NC} $OPTIMIZE"
  echo -e "${CYAN}Verbose:${NC} $VERBOSE"
  echo -e "${CYAN}Backup:${NC} $BACKUP"
  print_separator

  check_go_version
  verify_mobile_package
  check_android_env

  if ! check_gomobile; then
    install_gomobile
    init_gomobile
  fi

  print_separator
  mkdir -p "$BUILD_DIR"

  [ "$BACKUP" = true ] && backup_existing

  case "$COMMAND" in
    aar|"") build_aar ;;
    zip) build_aar; create_zip ;;
    *) log_error "Unknown command: $COMMAND"; echo "Run './build_gomobile_android.sh help'"; exit 1 ;;
  esac

  print_separator
  validate_aar

  local elapsed
  elapsed=$(get_elapsed_time)
  log_success "Build completed successfully in $(format_duration "$elapsed")"
  echo -e "Output: ${CYAN}$BUILD_DIR/$AAR_NAME${NC} ($(format_size "$BUILD_DIR/$AAR_NAME"))"
  print_separator
}

main
