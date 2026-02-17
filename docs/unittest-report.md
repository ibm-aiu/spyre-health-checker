# Unit Tests
Test item | Case description | File location
---|---|---
HealthChecker functions|GetVitalStates() returns v.States object|/internal/healthcheck/healthcheck_test.go
HealthChecker functions|UpdateStates() does not error with simple test scenario|/internal/healthcheck/healthcheck_test.go
HealthChecker functions|UpdateStates() is actually callable at Runtime|/internal/healthcheck/healthcheck_test.go
Parser|parseLSPCI() identifies supported cards, online/error state, and device type|/internal/healthcheck/parser_test.go
Server|can update healths|/pkg/server/server_test.go
Server|concurrent access to vitals is thread-safe|/pkg/server/server_test.go
Server|pseudo card health request|/pkg/server/server_test.go
Server/HTTP Health Check Endpoints|should handle multiple concurrent health check requests|/pkg/server/server_test.go
Server/HTTP Health Check Endpoints|should return 200 OK for /healthz endpoint|/pkg/server/server_test.go
Server/HTTP Health Check Endpoints|should return 200 OK for /readyz endpoint when server is ready|/pkg/server/server_test.go
Server/HTTP Health Check Endpoints/RegisterForSpyreDevicesEventsWithDevices|should add new devices to tracking map|/pkg/server/server_test.go
Server/HTTP Health Check Endpoints/RegisterForSpyreDevicesEventsWithDevices|should detect removed devices|/pkg/server/server_test.go
Server/HTTP Health Check Endpoints/RegisterForSpyreDevicesEventsWithDevices|should work with empty initial device list|/pkg/server/server_test.go
