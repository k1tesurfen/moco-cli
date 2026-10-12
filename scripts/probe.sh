#!/bin/bash
# Phase 0 probe: calls read-only MOCO endpoints with the personal API token and
# prints status, paging headers and the response shape (with truncated sample values).
# Never writes to MOCO. Raw responses are saved to .probe/ (gitignored).
#
# Token is read from the Keychain (service "moco-cli", account = subdomain). Store it once with:
#   security add-generic-password -U -s moco-cli -a <subdomain> -w
#
# Usage: scripts/probe.sh <subdomain>

set -u

SUB="${1:-${MOCO_SUBDOMAIN:-}}"
if [ -z "$SUB" ]; then
  echo "usage: $0 <subdomain>" >&2
  exit 2
fi

TOKEN="$(security find-generic-password -s moco-cli -a "$SUB" -w 2>/dev/null)"
if [ -z "$TOKEN" ]; then
  echo "No token in Keychain (service moco-cli, account $SUB)." >&2
  echo "Store it with: security add-generic-password -U -s moco-cli -a $SUB -w" >&2
  exit 1
fi

BASE="https://$SUB.mocoapp.com/api/v1"
OUT="$(cd "$(dirname "$0")/.." && pwd)/.probe"
mkdir -p "$OUT"

TODAY="$(date +%Y-%m-%d)"
WEEK_AGO="$(date -v-7d +%Y-%m-%d)"
MONTH_AGO="$(date -v-30d +%Y-%m-%d)"

# Shape of a JSON value: leaves become "type: sample", arrays show length + first element.
SHAPE='
def leaf: if type == "string" then "string: " + (if length > 40 then .[0:40] + "…" else . end)
          elif type == "null" then "null"
          else (type + ": " + tostring) end;
def shape: if type == "object" then with_entries(.value |= shape)
           elif type == "array" then (if length == 0 then "[] (empty)" else {"_len": length, "_first": (.[0] | shape)} end)
           else leaf end;
shape'

n=0
probe() {
  local path="$1"
  n=$((n + 1))
  local name
  name="$(printf "%02d" "$n")_$(printf "%s" "$path" | tr -c 'A-Za-z0-9' '_' | cut -c1-60)"
  local hdr="$OUT/$name.headers" body="$OUT/$name.json"

  local code
  code="$(curl -sS -o "$body" -D "$hdr" -w '%{http_code}' \
    -H "Authorization: Token token=$TOKEN" \
    -H "Accept: application/json" \
    "$BASE$path")"

  echo "================================================================"
  echo "GET $path  →  HTTP $code"
  grep -iE '^(x-total|x-page|x-per-page|link|retry-after|x-ratelimit[^:]*|ratelimit[^:]*):' "$hdr" | tr -d '\r' | sed 's/^/  /'
  if jq -e . "$body" >/dev/null 2>&1; then
    jq "$SHAPE" "$body"
  else
    echo "  (non-JSON body, first 300 bytes)"
    head -c 300 "$body"; echo
  fi
}

# Identity
probe "/session"

ME="$(jq -r '.id // empty' "$OUT/01__session.json" 2>/dev/null)"
echo
echo ">>> my user id: ${ME:-unknown}"

[ -n "$ME" ] && probe "/users/$ME"
probe "/users?per_page=2"

# Projects
probe "/projects/assigned?active=true"
probe "/projects?per_page=2"

# Presences: without and with user_id
probe "/users/presences?from=$WEEK_AGO&to=$TODAY"
[ -n "$ME" ] && probe "/users/presences?from=$WEEK_AGO&to=$TODAY&user_id=$ME"

# Activities: without and with user_id, plus pagination check
probe "/activities?from=$WEEK_AGO&to=$TODAY"
[ -n "$ME" ] && probe "/activities?from=$WEEK_AGO&to=$TODAY&user_id=$ME"
[ -n "$ME" ] && probe "/activities?from=$MONTH_AGO&to=$TODAY&user_id=$ME&per_page=2"

# Error format: unknown id
probe "/activities/1"

echo "================================================================"
echo "Raw responses saved in $OUT"
