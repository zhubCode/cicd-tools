#!/bin/bash
set -eou pipefail

# default timeout is 3 seconds, can be overriden
VERIFY_TIMEOUT=${1:-${VERIFY_TIMEOUT:-3}}

if ! /usr/bin/pgrep -f ".*register-the-runner"  > /dev/null && ! /usr/bin/pgrep -f "gitlab.*runner"  > /dev/null ; then
    exit 1
fi

status=0
# empty --url= helps `gitlab-runner verify` select all configured runners (otherwise filters for $CI_SERVER_URL)
verify_output=$(timeout "${VERIFY_TIMEOUT}" gitlab-runner verify --url= 2>&1) || status=$?

# timeout exit code is 143 with busybox, and 124 with coreutils
if (( status == 143 )) || (( status == 124 )) ; then
    echo "'gitlab-runner verify' terminated by timeout, not a conclusive failure" >&2
    exit 0
elif (( status > 0 )) ; then
    exit ${status}
fi

grep -qE "is (alive|valid)" <<<"${verify_output}"
