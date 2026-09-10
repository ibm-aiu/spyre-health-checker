/*
 * +-------------------------------------------------------------------+
 * | (C) Copyright IBM Corp. 2025, 2026                                |
 * | SPDX-License-Identifier: Apache-2.0                               |
 * +-------------------------------------------------------------------+
 */

package reporter

import (
	"context"
	"fmt"
	"time"

	cardhealth "github.com/ibm-aiu/spyre-health-checker/pkg/cardhealth"
	pb "github.com/ibm-aiu/spyre-health-checker/pkg/health/spyre"
	types "github.com/ibm-aiu/spyre-health-checker/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultCardHealthTimeout = 10 * time.Second

// CardHealthClient dials the co-located aiu-cardmgmt-health-api sidecar over
// its UNIX socket and queries per-slot health via GetCardHealth.
type CardHealthClient struct {
	socketPath string
	timeout    time.Duration
}

// NewCardHealthClient returns a CardHealthClient for the given UNIX socket path.
func NewCardHealthClient(socketPath string) *CardHealthClient {
	return &CardHealthClient{
		socketPath: socketPath,
		timeout:    defaultCardHealthTimeout,
	}
}

// CollectForSlots dials the sidecar, calls GetCardHealth for each PCI slot,
// and returns a DeviceState per slot. Slots that return an RPC error or a
// non-empty error_code from the sidecar are skipped so that a single
// unavailable slot does not abort the whole collect cycle.
func (c *CardHealthClient) CollectForSlots(slots []string) ([]types.DeviceState, error) {
	conn, err := grpc.NewClient("unix://"+c.socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("cardhealth: dial %s: %w", c.socketPath, err)
	}
	defer conn.Close() //nolint:errcheck

	stub := cardhealth.NewCardHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	states := make([]types.DeviceState, 0, len(slots))
	for _, slot := range slots {
		resp, err := stub.GetCardHealth(ctx, &cardhealth.GetCardHealthRequest{PciSlot: slot})
		if err != nil {
			// RPC-level failure for this slot — skip and continue.
			continue
		}
		if resp.Error != nil && resp.Error.ErrorCode != "" {
			// Application-level error reported by the sidecar — skip.
			continue
		}
		state := pb.DEVICE_STATE_IN_ERROR
		if resp.Health == "healthy" {
			state = pb.DEVICE_STATE_ONLINE
		}
		states = append(states, types.DeviceState{
			PciAddress: slot,
			Type:       pb.DEVICE_TYPE_PF,
			State:      state,
		})
	}
	return states, nil
}
