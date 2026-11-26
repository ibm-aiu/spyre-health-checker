# Future Enhancements

Work that was left out of a first version on purpose, with where it would live. Items about the node
health override itself (`healthy-since`, timestamps on events, a PF line that covers its VFs, RAS
recovery, an immediate re-check, a `SpyreClusterPolicy` field) are in the *Later* section of
[`node-health-override.md`](node-health-override.md) and are not repeated here.

## Showing and alerting on the override

The gauge `spyre_health_override{node, mode}` is already scraped: the operator's `ServiceMonitor`
`spyre-health-checker` takes the whole `/metrics` endpoint every 30 s. Nothing displays it or alerts on
it yet. It has three series per node (`healthy`, `unhealthy`, `ignored`), each a number of devices or
lines, and all three are 0 in the normal state, so both items below filter on a value above 0.

### Dashboard panel

- **Repo and file:** `spyre-operator`, `assets/state-init/common/0500_dashboard.yaml` (a `ConfigMap` in
  `openshift-config-managed` that holds the dashboard JSON).
- **Panel:** a table next to the existing *Device State Count by Node / State / Type* table, in the
  *Spyre Device State* row, with the query `spyre_health_override{node=~"$node"} > 0` and the
  `node`, `mode` and value columns. It then lists every node that has an override and how many
  devices it affects.
- **Care:** the dashboard is a large JSON document inside YAML. It is not known whether a test parses
  it, so load it in a console after editing.

### Alert rules

An example for the admin guide, or for a rule file shipped by the operator. The durations are examples:
choose them to match your maintenance windows.

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: spyre-health-override
  namespace: <operator-namespace>
spec:
  groups:
  - name: spyre-health-override
    rules:
    - alert: SpyreHealthOverrideHealthyActive
      # healthy hides real faults and never expires, so it must not be forgotten
      expr: spyre_health_override{mode="healthy"} > 0
      for: 12h
      labels:
        severity: warning
      annotations:
        summary: Spyre devices on {{ $labels.node }} are forced healthy
        description: >-
          The ConfigMap spyre-health-override forces {{ $value }} Spyre device(s) on {{ $labels.node }}
          to ONLINE, whatever the detectors report. Remove the line when the work is done.
    - alert: SpyreHealthOverrideIgnored
      # a typo in the ConfigMap is skipped, so that line silently does nothing
      expr: spyre_health_override{mode="ignored"} > 0
      for: 10m
      labels:
        severity: warning
      annotations:
        summary: Spyre health override lines of {{ $labels.node }} are not used
        description: >-
          {{ $value }} line(s) of the entry of {{ $labels.node }} in the ConfigMap spyre-health-override
          could not be applied (a typo, a short address, or a device that the node does not report).
          See the health checker log.
```

- **Pod restarts:** the series disappear while the health checker pod restarts, which should restart the
  `for` clock of the first rule. With no series there is no alert, so a health checker that is down
  needs its own alert.
- **Where the rule file lives:** if the operator shipped it, it would be a new asset in the operator
  namespace. Whether the operator's role allows `PrometheusRule` objects, and whether the platform
  Prometheus loads rules from that namespace, has not been checked.

### Checks still to do

- The PromQL and the rule file have not been run through `promtool check rules` or a live Prometheus.
- Confirm on a cluster that the gauge appears in Observe, Metrics after the first scrape.
