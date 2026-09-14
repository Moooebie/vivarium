package docker

import (
	"context"
	"fmt"
)

// NetworkName is the user-defined bridge network used by all instances.
const NetworkName = "vivarium-net"

// Network parameters (see BACKEND.md §2.2).
const (
	NetworkSubnet  = "172.28.0.0/16"
	NetworkGateway = "172.28.0.1"
)

// EnsureNetwork creates the vivarium-net bridge network if it does not exist
// and returns its ID.
func (c *Client) EnsureNetwork(ctx context.Context) (string, error) {
	var net Network
	err := c.getJSON(ctx, "/networks/"+escapePath(NetworkName), &net)
	if err == nil {
		return net.ID, nil
	}
	if !IsNotFound(err) {
		return "", fmt.Errorf("inspect network: %w", err)
	}
	req := networkCreateRequest{
		Name:           NetworkName,
		Driver:         "bridge",
		CheckDuplicate: true,
		IPAM: &ipamSpec{
			Driver: "default",
			Config: []ipamIPAMEntry{{Subnet: NetworkSubnet, Gateway: NetworkGateway}},
		},
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.postJSON(ctx, "/networks/create", req, &created); err != nil {
		return "", fmt.Errorf("create network: %w", err)
	}
	return created.ID, nil
}

// InspectNetwork returns a network by name.
func (c *Client) InspectNetwork(ctx context.Context, name string) (*Network, error) {
	var net Network
	if err := c.getJSON(ctx, "/networks/"+escapePath(name), &net); err != nil {
		return nil, err
	}
	return &net, nil
}
