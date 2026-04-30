# Unit Tests
Test item | Case description | File location
---|---|---
HealthChecker functions|GetVitalStates() returns v.States object|/internal/healthcheck/healthcheck_test.go
HealthChecker functions|UpdateStates() does not error with simple test scenario|/internal/healthcheck/healthcheck_test.go
HealthChecker functions|UpdateStates() is actually callable at Runtime|/internal/healthcheck/healthcheck_test.go
Parser|parseLSPCI() identifies supported cards, online/error state, and device type|/internal/healthcheck/parser_test.go
Prometheus metrics/InitMetrics|should handle double registration gracefully|/internal/utils/prometheus_test.go
Prometheus metrics/InitMetrics|should register Go and Process collectors|/internal/utils/prometheus_test.go
Prometheus metrics/InitMetrics|should register SpyreDeviceState metric successfully|/internal/utils/prometheus_test.go
Prometheus metrics/UpdateDeviceMetrics|should handle all device states correctly|/internal/utils/prometheus_test.go
Prometheus metrics/UpdateDeviceMetrics|should handle all device types correctly|/internal/utils/prometheus_test.go
Prometheus metrics/UpdateDeviceMetrics|should handle empty device list|/internal/utils/prometheus_test.go
Prometheus metrics/UpdateDeviceMetrics|should reset previous metrics when updating|/internal/utils/prometheus_test.go
Prometheus metrics/UpdateDeviceMetrics|should update metrics for a single device|/internal/utils/prometheus_test.go
Prometheus metrics/UpdateDeviceMetrics|should update metrics for multiple devices|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'BOOTING' for DEVICE_STATE_BOOTING|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'IN_ERROR' for DEVICE_STATE_IN_ERROR|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'OFFLINE' for DEVICE_STATE_OFFLINE|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'ONLINE' for DEVICE_STATE_ONLINE|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'REMOVED' for DEVICE_STATE_REMOVED|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'RUNNING_DIAGNOSTICS' for DEVICE_STATE_RUNNING_DIAGNOSTICS|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'SHUTTING_DOWN' for DEVICE_STATE_SHUTTING_DOWN|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'UNSPECIFIED' for DEVICE_STATE_UNSPECIFIED|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceState|should return 'UNSPECIFIED' for unknown device state|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceType|should return 'PF' for DEVICE_TYPE_PF|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceType|should return 'UNSPECIFIED' for DEVICE_TYPE_UNSPECIFIED|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceType|should return 'UNSPECIFIED' for unknown device type|/internal/utils/prometheus_test.go
Prometheus metrics/enumDeviceType|should return 'VF' for DEVICE_TYPE_VF|/internal/utils/prometheus_test.go
Server/amd64|pseudo card health (amd64)|/pkg/server/server_test.go
Server/general|can get pseudo card health request|/pkg/server/server_test.go
Server/general|can update healths|/pkg/server/server_test.go
Server/general|concurrent access to vitals is thread-safe|/pkg/server/server_test.go
Server/general/HTTP Health Check Endpoints|should handle multiple concurrent health check requests|/pkg/server/server_test.go
Server/general/HTTP Health Check Endpoints|should return 200 OK for /healthz endpoint|/pkg/server/server_test.go
Server/general/HTTP Health Check Endpoints|should return 200 OK for /metrics endpoint|/pkg/server/server_test.go
Server/general/HTTP Health Check Endpoints|should return 200 OK for /readyz endpoint when server is ready|/pkg/server/server_test.go
Server/general/RegisterForSpyreDevicesEventsWithDevices|should add new devices to tracking map|/pkg/server/server_test.go
Server/general/RegisterForSpyreDevicesEventsWithDevices|should detect removed devices|/pkg/server/server_test.go
Server/general/RegisterForSpyreDevicesEventsWithDevices|should work with empty initial device list|/pkg/server/server_test.go
Server/s390x|pseudo card health (s390x)|/pkg/server/server_test.go
