package cli

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRemoteHostLockReclaimRequiresExpiredLeaseAndInactiveBroker(t *testing.T) {
	expired := remoteHostLockResult{Locked: true, State: "active", LeaseExpired: true, LeaseVersion: "1", LockToken: "owner-token", BrokerStateDir: "C:/maya-stall/sessiond-ui", BrokerPython: "C:/maya-stall/python.exe", BrokerRepo: "C:/maya-stall/broker"}
	if remoteHostLockReclaimAllowed(expired, false) {
		t.Fatal("expired Host Lock was reclaimable while Session Broker remained active")
	}
	if !remoteHostLockReclaimAllowed(expired, true) {
		t.Fatal("expired Host Lock was not reclaimable after Session Broker proved inactive")
	}
	fresh := expired
	fresh.LeaseExpired = false
	if remoteHostLockReclaimAllowed(fresh, true) {
		t.Fatal("live Host Lock lease was reclaimable")
	}
	missingBrokerOwner := expired
	missingBrokerOwner.BrokerStateDir = ""
	if remoteHostLockReclaimAllowed(missingBrokerOwner, true) {
		t.Fatal("Host Lock without its owning Session Broker state directory was reclaimable")
	}
}

func TestRemoteHostLockRecoveryRequiresTrustedBrokerConfiguration(t *testing.T) {
	host := mayaHostConfig{Broker: brokerConfig{
		StateDir: "C:/maya-stall/sessiond-ui",
		Python:   "C:/maya-stall/python.exe",
		Repo:     "C:/maya-stall/broker",
	}}
	owner := remoteHostLockResult{
		BrokerStateDir: host.Broker.StateDir,
		BrokerPython:   host.Broker.Python,
		BrokerRepo:     host.Broker.Repo,
	}
	if !remoteHostLockBrokerConfigMatches(host, owner) {
		t.Fatal("matching trusted broker configuration was rejected")
	}
	variant := owner
	variant.BrokerStateDir = `c:\MAYA-STALL\SESSIOND-UI`
	variant.BrokerPython = `c:\MAYA-STALL\PYTHON.EXE`
	variant.BrokerRepo = `c:\MAYA-STALL\BROKER`
	if !remoteHostLockBrokerConfigMatches(host, variant) {
		t.Fatal("equivalent Windows broker paths were rejected")
	}
	relativeStateHost := host
	relativeStateHost.Broker.StateDir = "sessiond-ui"
	relativeStateOwner := owner
	relativeStateOwner.BrokerStateDir = `c:\maya-stall\broker\sessiond-ui`
	if !remoteHostLockBrokerConfigMatches(relativeStateHost, relativeStateOwner) {
		t.Fatal("relative trusted broker state directory did not match its absolute owner path")
	}
	commandPythonHost := host
	commandPythonHost.Broker.Python = "python"
	commandPythonOwner := owner
	commandPythonOwner.BrokerPython = "PYTHON"
	if !remoteHostLockBrokerConfigMatches(commandPythonHost, commandPythonOwner) {
		t.Fatal("trusted non-absolute broker Python command was rejected")
	}
	owner.BrokerPython = "C:/maya-stall/untrusted/python.exe"
	if remoteHostLockBrokerConfigMatches(host, owner) {
		t.Fatal("host-writable lock selected an untrusted broker executable")
	}
}

func TestHostLockContentionSmokeHeartbeatPrecedesLeaseExpiry(t *testing.T) {
	lease := 2 * time.Second
	interval := hostLockContentionHeartbeatInterval(lease)
	if interval <= 0 || interval >= lease {
		t.Fatalf("contention heartbeat interval = %s, want within lease %s", interval, lease)
	}
}

func TestSessiondStatusMustExplicitlyProveEveryProcessInactive(t *testing.T) {
	stopped := sessiondStatusResult{HasState: true, DerivedStatus: "stopped", ProcessAlive: map[string]bool{"daemon": false, "maya": false, "mcp": false}}
	stopped.State.Status = "stopped"
	if !sessiondStatusProvesInactive(stopped) {
		t.Fatal("explicit stopped broker with dead processes was not accepted as inactive")
	}
	missing := sessiondStatusResult{DerivedStatus: "missing", ProcessAlive: map[string]bool{}}
	if sessiondStatusProvesInactive(missing) {
		t.Fatal("missing broker state was accepted as proof of inactivity")
	}
	missing.ProcessAlive["maya"] = true
	if sessiondStatusProvesInactive(missing) {
		t.Fatal("missing broker state with a live Maya process was accepted as inactive")
	}
	if sessiondStatusProvesInactive(sessiondStatusResult{}) {
		t.Fatal("incomplete broker status was accepted as inactive")
	}
	incompleteStopped := stopped
	delete(incompleteStopped.ProcessAlive, "daemon")
	if sessiondStatusProvesInactive(incompleteStopped) {
		t.Fatal("stopped broker without complete process liveness was accepted as inactive")
	}
}

func TestHostLockHeartbeatKeepsFirstRenewalFailure(t *testing.T) {
	oldInterval := hostSideLockHeartbeatInterval
	hostSideLockHeartbeatInterval = 5 * time.Millisecond
	t.Cleanup(func() { hostSideLockHeartbeatInterval = oldInterval })
	want := errors.New("ownership lost")
	calls := 0
	stop, check := startHostLockHeartbeat(func() error {
		calls++
		if calls == 1 {
			return want
		}
		return nil
	})
	t.Cleanup(func() { _ = stop() })
	deadline := time.Now().Add(time.Second)
	for check() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(check(), want) {
		t.Fatalf("heartbeat error = %v, want %v", check(), want)
	}
	time.Sleep(15 * time.Millisecond)
	if !errors.Is(stop(), want) {
		t.Fatalf("stopped heartbeat error = %v, want sticky %v", stop(), want)
	}
}

func TestRenewHostLockDoesNotConvertKeptOwnerBackToLeased(t *testing.T) {
	const kept = "host: alpha\nlockToken: token\nkeptRun: kept-run\n"
	replaced := false
	lock := hostSideLock{
		expected: kept,
		replaceOwner: func(string, string) error {
			replaced = true
			return nil
		},
	}
	if err := lock.renew("alpha"); err != nil {
		t.Fatalf("renew kept Host Lock: %v", err)
	}
	if replaced {
		t.Fatal("renew rewrote a kept Host Lock")
	}
	if !strings.Contains(lock.expected, "keptRun: kept-run") {
		t.Fatalf("kept Host Lock changed: %q", lock.expected)
	}
}
