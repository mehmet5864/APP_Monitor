package probe

import (
	"context"
	"errors"
	"net"
)

func sshHostProbe(ctx context.Context, d *net.Dialer, spec Spec, block bool) (map[string]float64, error) {
	return nil, errors.New("SSH ile ölçüm bu sürümde etkin değil")
}
