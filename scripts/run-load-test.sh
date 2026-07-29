#!/bin/bash
# Interactive k6 load test runner for the rate limiter's Kubernetes deployment.
#
# Usage:
#   ./run-load-test.sh              (interactive menu)
#   ./run-load-test.sh low|medium|heavy|all   (non-interactive, for scripting)
#
# Portable: macOS + Linux, bash only, no GNU-coreutils-only commands
# (no `timeout`, no `mapfile`, no associative arrays — bash 3.2 safe,
# since macOS ships an old bash by default).
#
# Requires: bash, kubectl, grep — all checked up front, with a clear
# error message if anything's missing, rather than failing partway
# through a run.

set -uo pipefail
# Deliberately NOT using `set -e` here: this script runs multiple
# independent tiers and needs to continue past a single tier's failure
# rather than aborting the whole run. Every command whose failure matters
# is checked explicitly instead.

# ---------------------------------------------------------------------------
# Colors (portable via tput; degrade to no color if tput/TERM unavailable)
# ---------------------------------------------------------------------------

if command -v tput >/dev/null 2>&1 && [ -t 1 ] && tput colors >/dev/null 2>&1; then
  C_RESET=$(tput sgr0)
  C_BOLD=$(tput bold)
  C_GREEN=$(tput setaf 2)
  C_YELLOW=$(tput setaf 3)
  C_RED=$(tput setaf 1)
  C_CYAN=$(tput setaf 6)
else
  C_RESET=""; C_BOLD=""; C_GREEN=""; C_YELLOW=""; C_RED=""; C_CYAN=""
fi

info()    { echo "${C_CYAN}==>${C_RESET} $*"; }
success() { echo "${C_GREEN}✓${C_RESET} $*"; }
warn()    { echo "${C_YELLOW}⚠${C_RESET}  $*"; }
error()   { echo "${C_RED}✗${C_RESET} $*" >&2; }
header()  {
  echo ""
  echo "${C_BOLD}========================================================================${C_RESET}"
  echo "${C_BOLD}  $*${C_RESET}"
  echo "${C_BOLD}========================================================================${C_RESET}"
}

# ---------------------------------------------------------------------------
# Portable "run with timeout" — GNU `timeout` isn't available by default on
# macOS. This reimplements the same behavior with plain bash + kill.
# ---------------------------------------------------------------------------

run_with_timeout() {
  local secs="$1"
  shift
  "$@" &
  local cmd_pid=$!
  ( sleep "$secs"; kill -9 "$cmd_pid" 2>/dev/null ) &
  local watcher_pid=$!
  local status
  wait "$cmd_pid" 2>/dev/null
  status=$?
  kill "$watcher_pid" 2>/dev/null
  wait "$watcher_pid" 2>/dev/null
  return $status
}

# ---------------------------------------------------------------------------
# Pre-flight checks
# ---------------------------------------------------------------------------

preflight_checks() {
  if [ -z "${BASH_VERSION:-}" ]; then
    error "This script must be run with bash, not sh or another shell."
    echo "Try: bash $0"
    exit 1
  fi

  local missing=""
  for cmd in kubectl grep; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
      missing="${missing} ${cmd}"
    fi
  done
  if [ -n "$missing" ]; then
    error "Required command(s) not found on PATH:${missing}"
    exit 1
  fi

  if [ ! -e /dev/tty ]; then
    error "/dev/tty not available in this environment."
    echo "This script needs a real terminal (macOS, Linux, or WSL/Git Bash on Windows)."
    exit 1
  fi

  if ! kubectl cluster-info >/dev/null 2>&1; then
    error "kubectl cannot reach a cluster. Is your Kubernetes cluster running?"
    exit 1
  fi

  if ! kubectl get namespace "$NAMESPACE" >/dev/null 2>&1; then
    error "Namespace '${NAMESPACE}' does not exist."
    echo "Deploy the cluster first (see README: Running on Kubernetes)."
    exit 1
  fi

  if ! kubectl get configmap k6-script -n "$NAMESPACE" >/dev/null 2>&1; then
    error "ConfigMap 'k6-script' not found in namespace '${NAMESPACE}'."
    echo "Run: kubectl apply -f k8s/06a-k6-script-configmap.yaml"
    exit 1
  fi

  for manifest in "$MANIFEST_LOW" "$MANIFEST_MEDIUM" "$MANIFEST_HEAVY"; do
    if [ ! -f "$manifest" ]; then
      error "Manifest not found: ${manifest}"
      echo "Run this script from the project root."
      exit 1
    fi
  done

  success "Pre-flight checks passed"
}

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

NAMESPACE="ratelimiter"
MANIFEST_LOW="k8s/06b-k6-loadtest-low.yaml"
MANIFEST_MEDIUM="k8s/06c-k6-loadtest-medium.yaml"
MANIFEST_HEAVY="k8s/06d-k6-loadtest-heavy.yaml"

# ---------------------------------------------------------------------------
# Cleanup on interrupt — don't leave an orphaned Job running if the user
# Ctrl+C's out, or the terminal closes, mid-run.
# ---------------------------------------------------------------------------

CURRENT_JOB=""
cleanup_on_interrupt() {
  echo ""
  if [ -n "$CURRENT_JOB" ]; then
    warn "Interrupted — cleaning up job ${CURRENT_JOB}"
    kubectl delete job "$CURRENT_JOB" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1
  fi
  exit 130
}
trap cleanup_on_interrupt INT TERM

# ---------------------------------------------------------------------------
# Core logic for a single tier
# ---------------------------------------------------------------------------

run_tier() {
  local TIER="$1"
  local JOB_NAME="k6-loadtest-${TIER}"
  local MANIFEST=""

  case "$TIER" in
    low)    MANIFEST="$MANIFEST_LOW" ;;
    medium) MANIFEST="$MANIFEST_MEDIUM" ;;
    heavy)  MANIFEST="$MANIFEST_HEAVY" ;;
    *)      error "Unknown tier: ${TIER}"; return 1 ;;
  esac

  local RESULTS_DIR="results/${TIER}"
  mkdir -p "$RESULTS_DIR"

  header "TIER: ${TIER}"

  info "Cleaning up any previous ${JOB_NAME} job"
  kubectl delete job "$JOB_NAME" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1

  info "Applying ${MANIFEST}"
  if ! kubectl apply -f "$MANIFEST" >/dev/null; then
    error "Failed to apply ${MANIFEST} — skipping ${TIER} tier."
    return 1
  fi

  CURRENT_JOB="$JOB_NAME"

  info "Waiting for pod to be created and running"
  local waited=0
  until kubectl get pods -n "$NAMESPACE" -l "job-name=${JOB_NAME}" \
      -o jsonpath='{.items[0].status.phase}' 2>/dev/null | grep -q "Running"; do
    sleep 1
    waited=$((waited + 1))
    if [ "$waited" -ge 60 ]; then
      error "Pod for ${JOB_NAME} did not reach Running within 60s."
      kubectl get pods -n "$NAMESPACE" -l "job-name=${JOB_NAME}" 2>/dev/null
      CURRENT_JOB=""
      return 1
    fi
  done

  local POD_NAME
  POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l "job-name=${JOB_NAME}" \
    -o jsonpath='{.items[0].metadata.name}')
  success "Pod running: ${POD_NAME}"

  info "Streaming live k6 progress (stops automatically when the test finishes):"
  echo "------------------------------------------------------------------------"

  # Run kubectl logs -f in the background, writing to a file, rather than
  # piping it directly into grep. A direct pipe caused a real hang: with
  # a strict pipefail-style pipeline, the shell waits for EVERY process in
  # the pipe to exit, not just the one that matched. kubectl logs -f blocks
  # waiting for the *next* line, and the container produces none while
  # sleeping — so a direct pipe stalled for the full sleep duration even
  # after the match was already found. Polling a file and explicitly
  # killing the background process avoids depending on a blocked read
  # ever resolving on its own.
  local LIVE_LOG="${RESULTS_DIR}/.live_raw.log"
  : > "$LIVE_LOG"
  kubectl logs -n "$NAMESPACE" "$POD_NAME" -f > "$LIVE_LOG" 2>&1 &
  local LOG_PID=$!

  local last_size=0
  local elapsed=0
  local found=0
  while [ "$elapsed" -lt 240 ]; do
    if grep -q "sleeping so results can be copied out" "$LIVE_LOG" 2>/dev/null; then
      found=1
      break
    fi
    local current_size
    current_size=$(wc -c < "$LIVE_LOG" 2>/dev/null || echo 0)
    if [ "$current_size" -gt "$last_size" ]; then
      tail -c +"$((last_size + 1))" "$LIVE_LOG"
      last_size=$current_size
    fi
    sleep 0.5
    elapsed=$((elapsed + 1))
  done

  # Print any final lines not yet shown.
  local final_size
  final_size=$(wc -c < "$LIVE_LOG" 2>/dev/null || echo 0)
  if [ "$final_size" -gt "$last_size" ]; then
    tail -c +"$((last_size + 1))" "$LIVE_LOG"
  fi

  # Stop the background log stream. Don't block on `wait` with no timeout —
  # a process stuck in a network read can fail to exit promptly on SIGTERM.
  kill "$LOG_PID" 2>/dev/null
  ( sleep 1; kill -9 "$LOG_PID" 2>/dev/null ) &
  disown 2>/dev/null
  rm -f "$LIVE_LOG"
  echo "------------------------------------------------------------------------"

  if [ "$found" -eq 0 ]; then
    error "k6 did not finish within 120s — something is wrong."
    CURRENT_JOB=""
    return 1
  fi

  info "k6 finished — waiting briefly for export files to finish flushing to disk"
  sleep 3

  info "Capturing results"
  kubectl logs -n "$NAMESPACE" "$POD_NAME" > "${RESULTS_DIR}/console.txt" 2>/dev/null

  if ! run_with_timeout 60 kubectl cp "${NAMESPACE}/${POD_NAME}:/output/summary.json" "${RESULTS_DIR}/summary.json" 2>/dev/null; then
    warn "Could not copy summary.json within 60s (console.txt still saved)"
  fi

  if ! run_with_timeout 60 kubectl cp "${NAMESPACE}/${POD_NAME}:/output/report.html" "${RESULTS_DIR}/report.html" 2>/dev/null \
      || [ ! -s "${RESULTS_DIR}/report.html" ]; then
    warn "report.html missing or empty on first attempt, retrying after 5s..."
    sleep 5
    if ! run_with_timeout 60 kubectl cp "${NAMESPACE}/${POD_NAME}:/output/report.html" "${RESULTS_DIR}/report.html" 2>/dev/null \
        || [ ! -s "${RESULTS_DIR}/report.html" ]; then
      warn "Could not copy report.html after retry (console.txt still saved)"
    fi
  fi

  info "Cleaning up job"
  if ! run_with_timeout 30 kubectl delete job "$JOB_NAME" -n "$NAMESPACE" --ignore-not-found >/dev/null 2>&1; then
    warn "Job deletion timed out — may need manual cleanup: kubectl delete job ${JOB_NAME} -n ${NAMESPACE}"
  fi
  CURRENT_JOB=""

  success "Done: ${TIER} tier — results in ${RESULTS_DIR}/"
  echo ""
  echo "  Quick summary:"
  if [ -f "${RESULTS_DIR}/console.txt" ]; then
    grep -E "http_req_duration\.\.|checks_succeeded|http_reqs\.\." "${RESULTS_DIR}/console.txt" 2>/dev/null \
      | sed 's/^ */    /'
  fi
  return 0
}

# ---------------------------------------------------------------------------
# Interactive menu (used when no argument is passed)
# ---------------------------------------------------------------------------

show_menu() {
  echo "" >&2
  echo "${C_BOLD}Rate Limiter Load Test Runner${C_RESET}" >&2
  echo "Which tier would you like to run?" >&2
  echo "" >&2
  local options=("low" "medium" "heavy" "all" "quit")
  select opt in "${options[@]}"; do
    case "$opt" in
      low|medium|heavy|all)
        echo "$opt"
        return 0
        ;;
      quit)
        echo "quit"
        return 0
        ;;
      *)
        echo "Invalid choice, try again." >&2
        ;;
    esac
  done
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
  preflight_checks

  local TIER_ARG="${1:-}"

  if [ -z "$TIER_ARG" ]; then
    TIER_ARG=$(show_menu)
  fi

  if [ "$TIER_ARG" = "quit" ]; then
    echo "Bye."
    exit 0
  fi

  if [ "$TIER_ARG" != "low" ] && [ "$TIER_ARG" != "medium" ] && \
     [ "$TIER_ARG" != "heavy" ] && [ "$TIER_ARG" != "all" ]; then
    error "Invalid tier: ${TIER_ARG}"
    echo "Usage: $0 [low|medium|heavy|all]"
    exit 1
  fi

  local TIERS_TO_RUN
  if [ "$TIER_ARG" = "all" ]; then
    TIERS_TO_RUN="low medium heavy"
  else
    TIERS_TO_RUN="$TIER_ARG"
  fi

  local FAILED_TIERS=""
  local PASSED_TIERS=""

  for TIER in $TIERS_TO_RUN; do
    if run_tier "$TIER"; then
      PASSED_TIERS="${PASSED_TIERS} ${TIER}"
    else
      warn "${TIER} tier FAILED — continuing to next tier if any"
      FAILED_TIERS="${FAILED_TIERS} ${TIER}"
    fi
  done

  header "SUMMARY"
  if [ -n "$PASSED_TIERS" ]; then
    success "Passed:${PASSED_TIERS}"
  fi
  if [ -n "$FAILED_TIERS" ]; then
    error "Failed:${FAILED_TIERS}"
    exit 1
  fi
  echo ""
  success "All requested tiers completed successfully"
}

main "$@"
