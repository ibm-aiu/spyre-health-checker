# Unit Tests
Test item | Case description | File location
---|---|---
HealthChecker functions|GetVitalStates() returns v.States object|/internal/healthcheck/healthcheck_test.go
HealthChecker functions|UpdateStates() does not error with simple test scenario|/internal/healthcheck/healthcheck_test.go
HealthChecker functions|UpdateStates() is actually callable at Runtime|/internal/healthcheck/healthcheck_test.go
Parser|parseLSPCI() identifies supported cards, online/error state, and device type|/internal/healthcheck/parser_test.go
Server|can update healths|/pkg/server/server_test.go
Server|pseudo card health request|/pkg/server/server_test.go
