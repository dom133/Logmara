# Sentinel monitors redis1 as the initial master; if it's no longer master
# by the time a sentinel starts (e.g. restarting after an earlier failover),
# it self-corrects by asking redis1 for its current role and gossiping with
# the other sentinels - this static "redis1" starting point does not pin
# Sentinel to redis1 being master forever.
#
# resolve-hostnames/announce-hostnames are required in a container/overlay
# network setup: without them Sentinel tracks nodes by IP, which changes
# every time Swarm reschedules a task onto a different node.
sentinel monitor mymaster redis1 6379 2
sentinel down-after-milliseconds mymaster 5000
sentinel failover-timeout mymaster 10000
sentinel parallel-syncs mymaster 1
sentinel resolve-hostnames yes
sentinel announce-hostnames yes

# sentinel auth-pass mymaster <password> is appended at container start
# (see redis/sentinel_entrypoint.sh) - it is not stored in this file since
# it comes from Vault's HTTP API at container start, not from a read-only
# Swarm config.
