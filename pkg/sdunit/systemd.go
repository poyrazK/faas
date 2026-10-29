package sdunit

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	busName        = "org.freedesktop.systemd1"
	busPath        = dbus.ObjectPath("/org/freedesktop/systemd1")
	managerIface   = "org.freedesktop.systemd1.Manager"
	unitIface      = "org.freedesktop.systemd1.Unit"
	serviceIface   = "org.freedesktop.systemd1.Service"
	propertiesGet  = "org.freedesktop.DBus.Properties.Get"
	errNoSuchUnit  = "org.freedesktop.systemd1.NoSuchUnit"
	pollIntervalLo = time.Millisecond
	pollIntervalHi = 20 * time.Millisecond
)

// Systemd is a private connection to PID 1 on the system bus.
type Systemd struct {
	conn *dbus.Conn
}

var _ Manager = (*Systemd)(nil)

// Connect opens a private, authenticated system bus connection.
func Connect(ctx context.Context) (*Systemd, error) {
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("sdunit: connect system bus: %w", err)
	}
	return &Systemd{conn: conn}, nil
}

// Close closes the bus connection. Units it started keep running.
func (s *Systemd) Close() error { return s.conn.Close() }

func (s *Systemd) manager() dbus.BusObject { return s.conn.Object(busName, busPath) }

// StartService implements Manager.
func (s *Systemd) StartService(ctx context.Context, name string, svc Service) (Unit, error) {
	if !ValidServiceName(name) {
		return Unit{}, fmt.Errorf("sdunit: invalid service name %q", name)
	}
	if err := svc.validate(); err != nil {
		return Unit{}, err
	}
	var job dbus.ObjectPath
	// "fail": a start never replaces a queued job of the same unit.
	if err := s.manager().CallWithContext(ctx, managerIface+".StartTransientUnit", 0, name, "fail", svc.properties(), []auxUnit{}).Store(&job); err != nil {
		return Unit{}, fmt.Errorf("sdunit: start %s: %w", name, err)
	}
	return s.waitStarted(ctx, name)
}

// waitStarted polls until the unit runs. Before its start job runs a new
// unit is inactive with a job queued; a unit that is failed, inactive with
// no job, or already unloaded (CollectMode=inactive-or-failed) did not start.
func (s *Systemd) waitStarted(ctx context.Context, name string) (Unit, error) {
	interval := pollIntervalLo
	for {
		u, pending, err := s.unit(ctx, name, true)
		if err != nil {
			return Unit{}, err
		}
		switch {
		case !u.Exists(), u.ActiveState == "failed", u.ActiveState == "inactive" && !pending,
			u.ActiveState == "deactivating":
			return u, fmt.Errorf("%w: %s is %s/%s", ErrStartFailed, name, u.LoadState, u.ActiveState)
		case u.Active() && u.MainPID > 0:
			return u, nil
		}
		if err := sleep(ctx, &interval); err != nil {
			return u, fmt.Errorf("sdunit: wait for %s to start: %w", name, err)
		}
	}
}

// Stop implements Manager.
func (s *Systemd) Stop(ctx context.Context, name string) error {
	if !ValidServiceName(name) {
		return fmt.Errorf("sdunit: invalid service name %q", name)
	}
	var job dbus.ObjectPath
	err := s.manager().CallWithContext(ctx, managerIface+".StopUnit", 0, name, "replace").Store(&job)
	if errorName(err) == errNoSuchUnit {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sdunit: stop %s: %w", name, err)
	}
	interval := pollIntervalLo
	for {
		u, pending, err := s.unit(ctx, name, false)
		if err != nil {
			return err
		}
		// Unloaded, or stopped with nothing queued: its cgroup is gone.
		if !u.Exists() || (!pending && (u.ActiveState == "inactive" || u.ActiveState == "failed")) {
			return nil
		}
		if err := sleep(ctx, &interval); err != nil {
			return fmt.Errorf("sdunit: wait for %s to stop: %w", name, err)
		}
	}
}

// Unit implements Manager.
func (s *Systemd) Unit(ctx context.Context, name string) (Unit, error) {
	if !ValidServiceName(name) {
		return Unit{}, fmt.Errorf("sdunit: invalid service name %q", name)
	}
	u, _, err := s.unit(ctx, name, true)
	return u, err
}

// unit reads the unit's state and whether a job is queued for it. full also
// reads the run's identity: invocation, main pid and cgroup.
func (s *Systemd) unit(ctx context.Context, name string, full bool) (Unit, bool, error) {
	u := Unit{Name: name, LoadState: "not-found"}
	var path dbus.ObjectPath
	err := s.manager().CallWithContext(ctx, managerIface+".GetUnit", 0, name).Store(&path)
	if errorName(err) == errNoSuchUnit {
		return u, false, nil
	}
	if err != nil {
		return u, false, fmt.Errorf("sdunit: get unit %s: %w", name, err)
	}
	obj := s.conn.Object(busName, path)
	get := func(iface, prop string, into any) error {
		v, err := getProperty(ctx, obj, iface, prop)
		if errorName(err) == errNoSuchUnit {
			return errUnloaded
		}
		if err != nil {
			return fmt.Errorf("sdunit: %s %s.%s: %w", name, iface, prop, err)
		}
		if err := v.Store(into); err != nil {
			return fmt.Errorf("sdunit: %s %s.%s: %w", name, iface, prop, err)
		}
		return nil
	}
	var job struct {
		ID   uint32
		Path dbus.ObjectPath
	}
	err = errors.Join(get(unitIface, "LoadState", &u.LoadState), get(unitIface, "ActiveState", &u.ActiveState), get(unitIface, "Job", &job))
	if err == nil && full {
		var invocation []byte
		var pid uint32
		err = errors.Join(get(unitIface, "InvocationID", &invocation), get(serviceIface, "MainPID", &pid),
			get(serviceIface, "ControlGroup", &u.ControlGroup))
		u.InvocationID, u.MainPID = hex.EncodeToString(invocation), int(pid)
	}
	if errors.Is(err, errUnloaded) {
		// Collected between GetUnit and the reads.
		return Unit{Name: name, LoadState: "not-found"}, false, nil
	}
	return u, job.ID != 0, err
}

var errUnloaded = errors.New("unit unloaded")

func getProperty(ctx context.Context, obj dbus.BusObject, iface, prop string) (dbus.Variant, error) {
	var v dbus.Variant
	err := obj.CallWithContext(ctx, propertiesGet, 0, iface, prop).Store(&v)
	return v, err
}

// List implements Manager.
func (s *Systemd) List(ctx context.Context, pattern string) ([]string, error) {
	var units []struct {
		Name, Description, LoadState, ActiveState, SubState, Following string
		Path                                                           dbus.ObjectPath
		JobID                                                          uint32
		JobType                                                        string
		JobPath                                                        dbus.ObjectPath
	}
	if err := s.manager().CallWithContext(ctx, managerIface+".ListUnitsByPatterns", 0, []string{}, []string{pattern}).Store(&units); err != nil {
		return nil, fmt.Errorf("sdunit: list %s: %w", pattern, err)
	}
	names := make([]string, 0, len(units))
	for _, u := range units {
		names = append(names, u.Name)
	}
	return names, nil
}

func errorName(err error) string {
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) {
		return dbusErr.Name
	}
	var dbusErrPtr *dbus.Error
	if errors.As(err, &dbusErrPtr) && dbusErrPtr != nil {
		return dbusErrPtr.Name
	}
	return ""
}

func sleep(ctx context.Context, interval *time.Duration) error {
	t := time.NewTimer(*interval)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
	}
	*interval = min(*interval*2, pollIntervalHi)
	return nil
}

// Connected reports whether the bus connection is still open.
func (s *Systemd) Connected() bool { return s.conn.Connected() }
