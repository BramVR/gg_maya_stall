package cli

import (
	"fmt"
	"time"
)

type transportReadinessProber interface {
	ValidateTransportConfig() error
	ProbeTransport(time.Duration) error
}

type sessionBrokerReadinessProber interface {
	ProbeSessionBroker(time.Duration) error
}

func probeHostReadiness(host runHost, broker sessionBroker, hostID string, timeout time.Duration) error {
	transport, ok := host.(transportReadinessProber)
	if !ok {
		return fmt.Errorf("pre-run readiness failed at ssh layer for Maya Host %s: host adapter does not support bounded transport probing", hostID)
	}
	if err := transport.ValidateTransportConfig(); err != nil {
		return fmt.Errorf("pre-run readiness failed at ssh layer for Maya Host %s: %w; fix the SSH configuration for this Maya Host; see docs/setup/windows-maya-host.md#openssh-reachability", hostID, err)
	}
	if err := transport.ProbeTransport(timeout); err != nil {
		return fmt.Errorf("pre-run readiness failed at ssh layer for Maya Host %s: %w; fix SSH reachability for this Maya Host; see docs/setup/windows-maya-host.md#openssh-reachability", hostID, err)
	}
	sessionBroker, ok := broker.(sessionBrokerReadinessProber)
	if !ok {
		return fmt.Errorf("pre-run readiness failed at session-broker layer for Maya Host %s: Session Broker adapter does not support bounded status probing", hostID)
	}
	if err := sessionBroker.ProbeSessionBroker(timeout); err != nil {
		return fmt.Errorf("pre-run readiness failed at session-broker layer for Maya Host %s: %w; start or repair the Session Broker on this Maya Host; see docs/setup/windows-maya-host.md#session-broker", hostID, err)
	}
	return nil
}
