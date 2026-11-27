# Routing and egress

Code: proxy/internal/egress

The need this area answers is STK-261126-346B. It holds which way a
qube's traffic leaves: through its proxy's netvm or an egress of tailnet
exit nodes, how an exit node is judged healthy, the order of failover
and failback, and failing closed when none is up.
