set -eu
for tool in cat curl grep readlink touch; do
    command -v "$tool" >/dev/null || exit 125
done

if cat "$1" >/dev/null 2>&1; then
    printf 'Unexpected host file access\n'
    exit 1
fi
printf 'host-file-denied\n'

test "$(readlink /proc/self/ns/net)" != "$3"
if curl --noproxy '*' --connect-timeout 1 --max-time 2 --fail "$2" >/dev/null 2>&1; then
    printf 'Unexpected host endpoint access\n'
    exit 1
fi
printf 'host-network-denied\n'

if touch /etc/mapbox-probe-write 2>/dev/null; then exit 1; fi
if touch /workspace/mapbox-probe-write 2>/dev/null; then exit 1; fi
printf 'root-and-source-readonly\n'

test "$(cat /sys/fs/cgroup/cpu.max)" = '200000 100000'
test "$(cat /sys/fs/cgroup/memory.max)" = '1073741824'
test "$(cat /sys/fs/cgroup/memory.swap.max)" = '0'
test "$(cat /sys/fs/cgroup/pids.max)" = '256'
printf 'kernel-resource-limits-confirmed\n'

grep -Eq '^CapEff:[[:space:]]+0000000000000000$' /proc/self/status
grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/self/status
printf 'capabilities-and-privilege-restrictions-confirmed\n'
printf 'boundary-probes-passed\n'
