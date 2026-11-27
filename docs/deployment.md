# Kubernetes deployment

Both `spyre-health-checker` and `aiu-cardmgmt-health-api` run one instance per
node (DaemonSet). Because the `aiu-cardmgmt-health-api` sidecar runs inside the
**cardmgmt worker Pod** (not this one), the UNIX socket it creates on the host
filesystem must be made accessible to the `spyre-health-checker` Pod via a
`hostPath` volume.

## RBAC

The RAS reporter requires access to the Kubernetes API. The
`spyre-health-checker` ServiceAccount must be bound to a `ClusterRole`
(not a namespaced `Role`) containing:

```yaml
- apiGroups: [""]
  resources: ["pods", "pods/log"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["resource.k8s.io"]
  resources: ["resourceclaims"]
  verbs: ["get"]
```

A `ClusterRole` is required because the pod watch uses a
`fieldSelector: spec.nodeName=<NODE_NAME>` that spans all namespaces. `pods/log`
must be listed separately -- Kubernetes treats log streaming as a distinct
sub-resource.

The `resourceclaims` rule lets the reporter recognise Spyre pods that obtain
devices via DRA instead of an `ibm.com/spyre_*` resource request. A failed pod
is scanned if it requests an `ibm.com/spyre_*` resource, or if any
`ResourceClaim` it uses has an allocated device whose driver is
`spyre.ibm.com`. Otherwise it is skipped. If a claim cannot be read (e.g.
missing permission), it does not count as a Spyre claim.

Without this `ClusterRole`, the watch call returns `403 Forbidden` at startup,
the reporter logs a warning and permanently disables itself; all other reporters
continue to function normally.

The RBAC resources are managed in the `spyre-operator` repository.

## Node-level socket path

The `aiu-cardmgmt-health-api` sidecar writes its socket to
`/var/run/cardmgmt-health-check-api/health-check-api.sock` inside its container.
Mount the parent directory from the host into both Pods so the path is shared:

```
Host node filesystem
  /usr/local/etc/spyre-cardmgmt/         <- hostPath (node-level backing dir)

cardmgmt worker Pod                      spyre-health-checker Pod
  hostPath -> /var/run/cardmgmt-health-check-api   hostPath -> /var/run/cardmgmt-health-check-api
               health-check-api.sock  <-  created by aiu-cardmgmt-health-api at startup
```

## cardmgmt worker Pod (abbreviated)

The `aiu-cardmgmt-health-api` container must mount the host directory so its
socket file lands on the node filesystem:

```yaml
volumes:
  - name: health-check-api-socket
    hostPath:
      path: /usr/local/etc/spyre-cardmgmt
      type: DirectoryOrCreate
  - name: spyre-cert
    secret:
      secretName: spyre-cert
      optional: true

containers:
  - name: cardmgmt-worker
    # ... aiu-cardmgmt image

  - name: health-api
    # ... aiu-cardmgmt-health-api image
    env:
      - name: API_GRPC_SOCKET_PATH
        value: /var/run/cardmgmt-health-check-api/health-check-api.sock
    volumeMounts:
      - name: health-check-api-socket
        mountPath: /var/run/cardmgmt-health-check-api
      - name: spyre-cert
        mountPath: /etc/cardmgmt-health-check-api/certs
        readOnly: true
```

## spyre-health-checker Pod (abbreviated)

Mount the same host directory, the shared TLS certificates, and point the
reporter at the socket:

```yaml
volumes:
  - name: health-check-api-socket
    hostPath:
      path: /usr/local/etc/spyre-cardmgmt
      type: Directory        # created by the cardmgmt worker Pod; must exist before this Pod starts
  - name: spyre-certs
    secret:
      secretName: spyre-health-checker-certs   # contains tls.crt, tls.key, ca.crt

containers:
  - name: spyre-health-checker
    # ... spyre-health-checker image
    env:
      - name: NODE_NAME
        valueFrom:
          fieldRef:
            fieldPath: spec.nodeName   # required by the RAS pod watcher
      # Optional: a slot that card management does not know (status na) is left to the other reporters.
      # Set this to "true" to report it UNSPECIFIED instead, so that it is not offered. The flag
      # --cardhealth-strict-na=true (one argument) does the same and wins over this variable.
      # - name: CARDHEALTH_STRICT_NA
      #   value: "true"
    args:
      - --enabled-reporters=lspci,cardmgmt
      - --cardhealth-socket=/var/run/cardmgmt-health-check-api/health-check-api.sock
      # TLS flags apply to both Channel A (this server) and Channel B (sidecar client).
      # The default paths below match the volumeMount; override with SPYRE_TLS_* env vars
      # if you prefer not to use args.
      - --tls-cert=/etc/spyre-health-checker/certs/tls.crt
      - --tls-key=/etc/spyre-health-checker/certs/tls.key
      - --tls-ca=/etc/spyre-health-checker/certs/ca.crt
    volumeMounts:
      - name: health-check-api-socket
        mountPath: /var/run/cardmgmt-health-check-api
        readOnly: true
      - name: spyre-certs
        mountPath: /etc/spyre-health-checker/certs
        readOnly: true
```

> **Note:** The `aiu-cardmgmt-health-api` sidecar `chmod 0666`s its socket at
> startup so the health-checker container can reach it regardless of UID.
> The mTLS handshake then provides the actual authentication layer -- the socket
> permissions are only needed for the filesystem-level `connect(2)` call.
> Ensure the cardmgmt Pod starts (and the socket exists on the host) before
> the health-checker Pod attempts its first collect cycle.

## Health override

An administrator can force the health of the Spyre devices on one node, or of single cards on it, with
an entry in the optional ConfigMap `spyre-health-override`. Create it in the namespace where the
health checker runs. Each key is a node name. The value is either one word for the whole node, or a
list of lines.

**The whole node.** The value is `healthy` or `unhealthy`:

```bash
# create the ConfigMap, with worker-3 forced to IN_ERROR (its devices are not offered to workloads)
oc create configmap spyre-health-override -n <namespace> --from-literal=worker-3=unhealthy

# add or change an entry: worker-5 forced to ONLINE
oc patch configmap spyre-health-override -n <namespace> --type merge -p '{"data":{"worker-5":"healthy"}}'

# remove an entry: worker-3 goes back to normal
oc patch configmap spyre-health-override -n <namespace> --type json -p '[{"op":"remove","path":"/data/worker-3"}]'
```

**Single cards.** The value has one line per card: the PCI address of the card (its physical function,
PF), then `healthy` or `unhealthy`. The line covers the card and its virtual functions (VFs). A PCI
address cannot be a ConfigMap key, so these lines go in the value of the node's key. Write the value in
a file and load it from the file:

```text
# worker-3: card 1a is being replaced, card 41 shows a false error
0000:1a:00.0 unhealthy
0000:41:00.0 healthy
```

```bash
# create the ConfigMap with this entry
oc create configmap spyre-health-override -n <namespace> --from-file=worker-3=worker-3.txt

# add or change the entry of one node in an existing ConfigMap
oc set data configmap/spyre-health-override -n <namespace> --from-file=worker-3=worker-3.txt
```

If you write the ConfigMap as YAML instead, put the lines in a block value (`worker-3: |`), as `oc`
does when it loads the file.

The rules for the value:

- A line is `<address> <value>`. The address is the PF of a card, in the full form with the four-digit
  domain, for example `0000:1a:00.0`; capitals are accepted. The value is `healthy` or `unhealthy`.
- A line covers the card and its VFs. The address of a card always ends in `.0`; the VFs of a card are
  the other functions in the same PCI slot, for example `0000:1a:00.1` and `0000:1a:00.2` for the card
  `0000:1a:00.0`. They get the same state as the card.
- A line for a VF is refused: it is skipped, a warning names the PF to use, and the VF follows the line of
  its card or the line for the whole node. A VF that has no PF (an isolated VF) cannot be named at all;
  only the whole-node line reaches it.
- A line with only `healthy` or `unhealthy`, or `all healthy` or `all unhealthy`, covers every device of
  the node. A line for a card wins over it, so `healthy` with the line `0000:1a:00.0 unhealthy` means
  everything is healthy except card 1a and its VFs.
- Text after `#` is a comment, and blank lines are ignored. If a card or `all` appears twice, the last
  line counts.
- A line that cannot be used (a typo, a short address, an unknown word) is skipped and a warning is logged
  once. The other lines still apply. So does a line for a card that the node does not report: the
  override never adds a device.
- With no ConfigMap, no entry for the node, an entry with only comments, or no mount at all, nothing
  changes.
- The health checker reads its entry at every check (`--timer`, 30 seconds when the operator installs
  it). A change takes effect at the first check after kubelet has updated the mounted file, so allow a
  couple of minutes.

**Seeing the override.** The health checker logs a warning each time an override starts, changes or
ends, and names the cards. The forced devices have `source="override"` in `spyre_device_state`. The
metric `spyre_health_override{node,mode}` counts them: `mode="healthy"` and `mode="unhealthy"` give the
number of devices forced to that state (a card with two VFs counts as three), and `mode="ignored"` counts
the lines that could not be used plus the devices that a `healthy` line could not be applied to (no
driver). All three are 0 when there is no override.

**Cautions.**

- `healthy` has no expiry and can hide real faults. Remove the line when the work is done, and consider
  an alert on `mode="healthy"` above 0.
- A card or VF that has lost its driver stays `IN_ERROR` even when a line says `healthy`. The health
  checker checks each function on its own and warns when a card's line could not be applied to one of
  them.
- Whoever can edit the ConfigMap can take a node or a card out of service or expose faulty cards. Limit
  who may update it.

**Wiring.** The health checker DaemonSet of the operator already mounts the ConfigMap. In another
deployment, mount it as an optional, read-only volume at `/etc/spyre-health-checker/override`. Do not
use `subPath`: kubelet does not update a file that is mounted with it. The health checker reads the file
named after `NODE_NAME` and needs no Kubernetes API permission for this.

```yaml
volumes:
  - name: health-override
    configMap:
      name: spyre-health-override
      optional: true
containers:
  - name: spyre-health-checker
    volumeMounts:
      - name: health-override
        mountPath: /etc/spyre-health-checker/override
        readOnly: true
```

See [Health indicators](health-indicators.md#stage-3----node-health-override-optional) for the exact
rules.

## Further reading

- [TLS reference](tls.md) -- certificate requirements, flag-to-file mapping,
  production guidance, and troubleshooting table for both channels.
- [Reporters](reporters.md) -- cardmgmt reporter flags and sidecar communication
  details.
- [Configuration reference](configuration.md) -- full flag and environment
  variable reference.
