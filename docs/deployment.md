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
```

A `ClusterRole` is required because the pod watch uses a
`fieldSelector: spec.nodeName=<NODE_NAME>` that spans all namespaces. `pods/log`
must be listed separately -- Kubernetes treats log streaming as a distinct
sub-resource.

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

## Further reading

- [TLS reference](tls.md) -- certificate requirements, flag-to-file mapping,
  production guidance, and troubleshooting table for both channels.
- [Reporters](reporters.md) -- cardmgmt reporter flags and sidecar communication
  details.
- [Configuration reference](configuration.md) -- full flag and environment
  variable reference.
