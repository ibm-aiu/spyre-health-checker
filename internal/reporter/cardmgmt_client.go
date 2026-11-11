/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	cardhealth "github.com/ibm-aiu/spyre-health-checker/pkg/cardhealth"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const defaultCardHealthTimeout = 10 * time.Second

// Values of GetCardHealthResponse.Health sent by the aiu-cardmgmt-health-api sidecar.
//
// Sidecar 0.0.4 and earlier collapse the card management status into two values:
// "healthy" (status online) and "unhealthy" (anything else).
// Sidecar 0.0.5 and later return the card management getCardStatus() status as is
// (PF boot status plus the privileged VF open status), or "na" when there is none.
const (
	healthHealthy   = "healthy"   // 0.0.4: card online
	healthUnhealthy = "unhealthy" // 0.0.4: any other status

	healthOnline      = "online"       // PF booted and privileged VF opened
	healthOffline     = "offline"      // PF offline
	healthFailed      = "failed"       // PF or privileged VF failed
	healthPFQuiescing = "pf-quiescing" // PF being quiesced (card management: discovered, not yet managed)
	healthStarting    = "starting"     // boot in progress
	healthPFCheck     = "pfcheck"      // boot step: PF being checked
	healthPFBooted    = "pfbooted"     // boot step: PF booted, privileged VF not yet opened
	healthNA          = "na"           // card management has no status for the slot (reported as UNSPECIFIED)
)

// mapCardStatus converts a sidecar Health value to a device state.
//
// The detailed statuses of sidecar 0.0.5 and later are mapped to the closest device state:
//
//	online                                -> ONLINE
//	offline                               -> OFFLINE
//	starting, pfcheck, pfbooted           -> BOOTING
//	pf-quiescing                          -> SHUTTING_DOWN
//	failed                                -> IN_ERROR
//	na (no status for the slot)           -> UNSPECIFIED
//
// The two values of sidecar 0.0.4 map as "healthy" -> ONLINE and "unhealthy" -> IN_ERROR (no detail).
// Only "healthy" and "online" are ONLINE, so a card in any other state is never offered to a workload.
// A value this version does not know is IN_ERROR.
func mapCardStatus(health string) pb.DEVICE_STATE {
	switch health {
	case healthHealthy, healthOnline:
		return pb.DEVICE_STATE_ONLINE
	case healthOffline:
		return pb.DEVICE_STATE_OFFLINE
	case healthStarting, healthPFCheck, healthPFBooted:
		return pb.DEVICE_STATE_BOOTING
	case healthPFQuiescing:
		return pb.DEVICE_STATE_SHUTTING_DOWN
	case healthNA:
		return pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED
	case healthFailed, healthUnhealthy:
		return pb.DEVICE_STATE_IN_ERROR
	default:
		return pb.DEVICE_STATE_DEVICE_STATE_UNSPECIFIED
	}
}

// CardHealthClient queries the aiu-cardmgmt-health-api sidecar via mTLS gRPC.
type CardHealthClient struct {
	socketPath  string
	timeout     time.Duration
	tlsCertPath string
	tlsKeyPath  string
	tlsCAPath   string
	// tlsServerName must match a SAN/CN in the server cert (e.g. "spyre-components").
	tlsServerName string
	// debug enables per-slot gRPC call logging to stdout.
	debug bool
}

// NewCardHealthClient returns a CardHealthClient configured for mTLS.
// tlsServerName must match a SAN/CN in the server cert.
func NewCardHealthClient(socketPath, tlsCertPath, tlsKeyPath, tlsCAPath, tlsServerName string) *CardHealthClient {
	return &CardHealthClient{
		socketPath:    socketPath,
		timeout:       defaultCardHealthTimeout,
		tlsCertPath:   tlsCertPath,
		tlsKeyPath:    tlsKeyPath,
		tlsCAPath:     tlsCAPath,
		tlsServerName: tlsServerName,
	}
}

// SetDebug enables per-slot gRPC call logging to stdout (--debug flag).
func (c *CardHealthClient) SetDebug(enabled bool) {
	c.debug = enabled
}

// buildMTLSCredentials loads cert/key/CA from disk and returns mTLS credentials.
func (c *CardHealthClient) buildMTLSCredentials() (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(c.tlsCertPath, c.tlsKeyPath)
	if err != nil {
		return nil, fmt.Errorf("cardhealth: load client cert/key: %w", err)
	}
	caPEM, err := os.ReadFile(c.tlsCAPath)
	if err != nil {
		return nil, fmt.Errorf("cardhealth: read CA cert: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("cardhealth: no valid CA certificate found in %s", c.tlsCAPath)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   c.tlsServerName,
		MinVersion:   tls.VersionTLS12,
	}
	return credentials.NewTLS(tlsCfg), nil
}

// CollectForSlots queries GetCardHealth for each slot over mTLS.
// Slots with RPC or application-level errors are skipped, not fatal.
func (c *CardHealthClient) CollectForSlots(slots []string) ([]types.DeviceState, error) {
	creds, err := c.buildMTLSCredentials()
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient("unix://"+c.socketPath, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("cardhealth: dial %s: %w", c.socketPath, err)
	}
	defer conn.Close() //nolint:errcheck

	stub := cardhealth.NewCardHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	if c.debug {
		fmt.Printf("cardhealth-debug: dialing %s with %d slot(s)\n", c.socketPath, len(slots))
	}

	var (
		firstRPCErr     error
		firstSidecarErr string
	)
	states := make([]types.DeviceState, 0, len(slots))
	for _, slot := range slots {
		state, sidecarErr, rpcErr := c.querySlot(ctx, stub, slot)
		if rpcErr != nil {
			if firstRPCErr == nil {
				firstRPCErr = rpcErr
			}
			continue
		}
		if sidecarErr != "" {
			if firstSidecarErr == "" {
				firstSidecarErr = sidecarErr
			}
			continue
		}
		states = append(states, *state)
	}

	logSkipped(len(slots), len(states), firstRPCErr, firstSidecarErr)

	if c.debug {
		fmt.Printf("cardhealth-debug: collect complete -- %d/%d slots returned a state\n",
			len(states), len(slots))
	}

	return states, nil
}

// querySlot calls GetCardHealth for a single slot and returns the DeviceState
// on success, or a non-empty sidecarErr / non-nil error on failure.
func (c *CardHealthClient) querySlot(
	ctx context.Context,
	stub cardhealth.CardHealthClient,
	slot string,
) (*types.DeviceState, string, error) {
	resp, err := stub.GetCardHealth(ctx, &cardhealth.GetCardHealthRequest{PciSlot: slot})
	if err != nil {
		if c.debug {
			fmt.Printf("cardhealth-debug: slot %-16s  RPC error: %v\n", slot, err)
		}
		return nil, "", err
	}
	if resp.Error != nil && resp.Error.ErrorCode != "" {
		msg := fmt.Sprintf("code=%s msg=%s", resp.Error.ErrorCode, resp.Error.ErrorMessage)
		if c.debug {
			fmt.Printf("cardhealth-debug: slot %-16s  sidecar error: %s\n", slot, msg)
		}
		return nil, msg, nil
	}
	s := mapCardStatus(resp.Health)
	if c.debug {
		fmt.Printf("cardhealth-debug: slot %-16s  health=%-9s  state=%s\n", slot, resp.Health, s)
	}
	return &types.DeviceState{PciAddress: slot, Type: pb.DEVICE_TYPE_PF, State: s}, "", nil
}

// logSkipped emits a one-line summary warning when slots were skipped.
func logSkipped(total, succeeded int, rpcErr error, sidecarErr string) {
	skipped := total - succeeded
	if skipped == 0 {
		return
	}
	switch {
	case rpcErr != nil:
		fmt.Printf("cardhealth: %d/%d slot(s) skipped -- RPC error (first: %v)\n", skipped, total, rpcErr)
	case sidecarErr != "":
		fmt.Printf("cardhealth: %d/%d slot(s) skipped -- sidecar error (first: %s)\n", skipped, total, sidecarErr)
	}
}
