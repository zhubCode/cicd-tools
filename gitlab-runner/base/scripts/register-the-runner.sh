#!/bin/sh
signal_handler() {
    if [ ! -d "/proc/$register_pid" ]; then
    wait $register_pid
    fi
    exit
}
trap 'signal_handler' QUIT INT

MAX_REGISTER_ATTEMPTS=30

# Reset/unset the not needed flags when an authentication token
RUN_UNTAGGED=""
ACCESS_LEVEL=""

if [ -n "$CI_SERVER_TOKEN" ] && [ "${CI_SERVER_TOKEN#glrt-}" != "$CI_SERVER_TOKEN" ]; then
    RUN_UNTAGGED=""
    ACCESS_LEVEL=""
    unset REGISTER_LOCKED
    unset RUNNER_TAG_LIST
fi

for i in $(seq 1 "${MAX_REGISTER_ATTEMPTS}"); do
    echo "Registration attempt ${i} of ${MAX_REGISTER_ATTEMPTS}"
    /entrypoint register \
    ${RUN_UNTAGGED} \
    ${ACCESS_LEVEL} \
    --template-config /configmaps/config.template.toml \
    --non-interactive &

    register_pid=$!
    wait $register_pid
    retval=$?

    if [ ${retval} = 0 ]; then
    break
    elif [ ${i} = ${MAX_REGISTER_ATTEMPTS} ]; then
    exit 1
    fi

    sleep 5
done
