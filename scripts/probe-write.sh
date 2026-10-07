#!/bin/bash
# Phase 2 write probe: creates, changes and deletes presences and one activity on an EMPTY past
# day, booking only on the internal project "Intern – nicht verrechenbar". Everything created is
# deleted again at the end. Refuses to run on today or on a day that already has entries.
#
# Usage: scripts/probe-write.sh <subdomain> <YYYY-MM-DD>

set -u

SUB="$1"
DAY="$2"
PROJECT_ID=946183239 # Intern – nicht verrechenbar (P.K0001.22.0110)
TASK_ID=15587130     # Programmierung

TOKEN="$(security find-generic-password -s moco-cli -a "$SUB" -w 2>/dev/null)" || { echo "no token" >&2; exit 1; }
BASE="https://$SUB.mocoapp.com/api/v1"
ME="$(curl -s -H "Authorization: Token token=$TOKEN" "$BASE/session" | jq -r .id)"

if [ "$DAY" = "$(date +%Y-%m-%d)" ] || [[ "$DAY" > "$(date +%Y-%m-%d)" ]]; then
  echo "refusing: $DAY is not in the past" >&2; exit 1
fi

call() { # method path [json]
  local method="$1" path="$2" body="${3:-}"
  echo "---- $method $path ${body}"
  local args=(-s -w '\n  → HTTP %{http_code}\n' -X "$method" -H "Authorization: Token token=$TOKEN" -H "Accept: application/json")
  [ -n "$body" ] && args+=(-H "Content-Type: application/json" -d "$body")
  curl "${args[@]}" "$BASE$path" | sed 's/^/  /'
}

count() { # path → number of entries on DAY
  curl -s -H "Authorization: Token token=$TOKEN" "$BASE/$1?from=$DAY&to=$DAY&user_id=$ME" | jq length
}

if [ "$(count users/presences)" != 0 ] || [ "$(count activities)" != 0 ]; then
  echo "refusing: $DAY already has entries" >&2; exit 1
fi
echo "$DAY is empty, user $ME. Starting."

json_id() { jq -r '.id // empty' 2>/dev/null; }
mkid() { # method path json → prints response, stores id in $LAST_ID
  local out
  out="$(curl -s -X "$1" -H "Authorization: Token token=$TOKEN" -H "Accept: application/json" -H "Content-Type: application/json" -d "$3" -w '\n%{http_code}' "$BASE$2")"
  echo "---- $1 $2 $3"
  echo "$out" | sed '$d' | sed 's/^/  /'
  echo "  → HTTP $(echo "$out" | tail -n1)"
  LAST_ID="$(echo "$out" | sed '$d' | json_id)"
}

echo; echo "== Presences =="
mkid POST /users/presences "{\"date\":\"$DAY\",\"from\":\"08:00\"}"
P1="$LAST_ID"
[ -n "$P1" ] && call PATCH "/users/presences/$P1" '{"to":"13:00"}'
mkid POST /users/presences "{\"date\":\"$DAY\",\"from\":\"14:00\",\"to\":\"17:07\",\"is_home_office\":true}"
P2="$LAST_ID"
echo "(overlap 12:00–15:00:)"
mkid POST /users/presences "{\"date\":\"$DAY\",\"from\":\"12:00\",\"to\":\"15:00\"}"
P3="$LAST_ID"
echo "(invalid time:)"
mkid POST /users/presences "{\"date\":\"$DAY\",\"from\":\"8\"}"
P4="$LAST_ID"
echo "(second open presence while 14:00 one is closed — open from 18:00:)"
mkid POST /users/presences "{\"date\":\"$DAY\",\"from\":\"18:00\"}"
P5="$LAST_ID"
echo "(another open presence while one is open:)"
mkid POST /users/presences "{\"date\":\"$DAY\",\"from\":\"19:00\"}"
P6="$LAST_ID"
echo "(list:)"
curl -s -H "Authorization: Token token=$TOKEN" "$BASE/users/presences?from=$DAY&to=$DAY&user_id=$ME" | jq -c '.[] | {id,from,to,is_home_office}' | sed 's/^/  /'

echo; echo "== Activity =="
mkid POST /activities "{\"date\":\"$DAY\",\"project_id\":$PROJECT_ID,\"task_id\":$TASK_ID,\"seconds\":900,\"description\":\"moco-cli API test – will be deleted\"}"
A1="$LAST_ID"
[ -n "$A1" ] && call PATCH "/activities/$A1" '{"seconds":1800,"description":"moco-cli API test (edited) – will be deleted"}'
echo "(missing task:)"
mkid POST /activities "{\"date\":\"$DAY\",\"project_id\":$PROJECT_ID,\"seconds\":900,\"description\":\"x\"}"
A2="$LAST_ID"
echo "(timer on past day:)"
[ -n "$A1" ] && call PATCH "/activities/$A1/start_timer"

echo; echo "== Cleanup =="
for id in $P1 $P2 $P3 $P4 $P5 $P6; do call DELETE "/users/presences/$id"; done
for id in $A1 $A2; do call DELETE "/activities/$id"; done
echo "remaining on $DAY: presences=$(count users/presences) activities=$(count activities)"
