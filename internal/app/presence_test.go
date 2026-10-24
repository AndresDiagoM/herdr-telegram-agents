package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/permgps/herdr-telegram-agents/internal/app"
	"github.com/permgps/herdr-telegram-agents/internal/domain"
	"github.com/permgps/herdr-telegram-agents/internal/testkit"
)

type presenceFixture struct {
	idle  *testkit.FakeIdle
	clock *testkit.FakeClock
	opts  *app.Options
	p     *app.Presence
}

func newPresence(t *testing.T, idle *testkit.FakeIdle) *presenceFixture {
	t.Helper()
	store := testkit.NewMemOptionsStore()
	quietOn, err := domain.DefaultOptions().With(domain.OptionQuietEnabled, "true")
	if err != nil {
		t.Fatal(err)
	}
	store.Set(quietOn) // quiet mode is off by default; these tests exercise it on
	opts := app.NewOptions(store.Stored(), store, nil, nil)
	clock := testkit.NewFakeClock(t0)
	return &presenceFixture{idle: idle, clock: clock, opts: opts, p: app.NewPresence(idle, opts, clock, nil)}
}

// change returns the pending change, or ok false when none is queued.
func (f *presenceFixture) change() (quiet, ok bool) {
	select {
	case v := <-f.p.Changes():
		return v, true
	default:
		return false, false
	}
}

func TestPresenceAtDeskAndAway(t *testing.T) {
	f := newPresence(t, testkit.NewFakeIdle(5*time.Second))
	ctx := context.Background()
	if f.p.Quiet() {
		t.Fatal("quiet before the first sample")
	}
	f.p.Poll(ctx)
	if !f.p.Quiet() {
		t.Fatal("not quiet while at the desk")
	}
	if v, ok := f.change(); !ok || !v {
		t.Fatalf("change after first poll = %v, %v", v, ok)
	}
	st := f.p.State()
	if !st.Enabled || !st.Supported || !st.AtDesk || st.ManualAway || !st.Quiet || st.Word() != "on" {
		t.Errorf("state = %+v", st)
	}
	f.p.Poll(ctx) // same verdict, no change queued
	if _, ok := f.change(); ok {
		t.Error("change queued without a flip")
	}

	f.idle.Set(3*time.Minute + time.Second)
	f.p.Poll(ctx)
	if f.p.Quiet() {
		t.Fatal("still quiet past the threshold")
	}
	if v, ok := f.change(); !ok || v {
		t.Fatalf("change after leaving = %v, %v", v, ok)
	}
	if got := f.p.State().Word(); got != "away" {
		t.Errorf("Word = %q", got)
	}
	// Exactly the threshold counts as away.
	f.idle.Set(3 * time.Minute)
	f.p.Poll(ctx)
	if f.p.Quiet() {
		t.Error("idle == threshold should be away")
	}
}

func TestPresenceFlapKeepsLatestChange(t *testing.T) {
	f := newPresence(t, testkit.NewFakeIdle(time.Second))
	ctx := context.Background()
	f.p.Poll(ctx) // quiet on, change queued
	f.idle.Set(time.Hour)
	f.p.Poll(ctx) // quiet off, replaces the queued value
	v, ok := f.change()
	if !ok || v {
		t.Fatalf("change = %v, %v; want latest (false)", v, ok)
	}
	if _, ok := f.change(); ok {
		t.Error("a second change was queued")
	}
}

func TestPresenceManualAway(t *testing.T) {
	f := newPresence(t, testkit.NewFakeIdle(time.Second))
	ctx := context.Background()
	f.p.Poll(ctx)
	f.change()

	st := f.p.Away(0, 7)
	if !st.ManualAway || !st.Until.IsZero() || st.Quiet || st.Word() != "away-manual" {
		t.Fatalf("state after /away = %+v", st)
	}
	if v, ok := f.change(); !ok || v {
		t.Fatalf("change after /away = %v, %v", v, ok)
	}
	f.p.Poll(ctx) // still at the desk, still overridden
	if f.p.Quiet() {
		t.Error("quiet despite /away")
	}
	st = f.p.Here(7)
	if st.ManualAway || !st.Quiet {
		t.Fatalf("state after /here = %+v", st)
	}
	if v, ok := f.change(); !ok || !v {
		t.Fatalf("change after /here = %v, %v", v, ok)
	}

	st = f.p.Away(2*time.Hour, 7)
	if !st.Until.Equal(t0.Add(2 * time.Hour)) {
		t.Fatalf("until = %v", st.Until)
	}
	f.change()
	f.clock.Advance(time.Hour)
	f.p.Poll(ctx)
	if f.p.Quiet() {
		t.Error("timed away expired early")
	}
	f.clock.Advance(time.Hour)
	f.p.Poll(ctx)
	if !f.p.Quiet() || f.p.State().ManualAway {
		t.Errorf("timed away did not expire: %+v", f.p.State())
	}
	if v, ok := f.change(); !ok || !v {
		t.Fatalf("change after expiry = %v, %v", v, ok)
	}
}

func TestPresenceOptionChanges(t *testing.T) {
	f := newPresence(t, testkit.NewFakeIdle(time.Second))
	ctx := context.Background()
	f.p.Poll(ctx)
	f.change()

	if err := f.opts.Set(ctx, domain.OptionQuietEnabled, "false", 7); err != nil {
		t.Fatal(err)
	}
	f.p.Recompute()
	if f.p.Quiet() {
		t.Fatal("quiet with the option off")
	}
	if v, ok := f.change(); !ok || v {
		t.Fatalf("change after disabling = %v, %v", v, ok)
	}
	if got := f.p.State().Word(); got != "off" {
		t.Errorf("Word = %q", got)
	}
	if err := f.opts.Set(ctx, domain.OptionQuietEnabled, "true", 7); err != nil {
		t.Fatal(err)
	}
	f.p.Recompute()
	if !f.p.Quiet() {
		t.Fatal("not quiet after re-enabling")
	}

	// A shorter threshold takes effect on the next sample.
	f.idle.Set(90 * time.Second)
	if err := f.opts.Set(ctx, domain.OptionQuietIdleMinutes, "1", 7); err != nil {
		t.Fatal(err)
	}
	f.change()
	f.p.Poll(ctx)
	if f.p.Quiet() {
		t.Error("90 s idle should be away with a 1 min threshold")
	}
}

// TestPresenceNoSourceDeskDefault covers a graphical session without an
// idle source (KDE Plasma on Wayland): quiet starts at the desk, /away
// releases it, a timed /away hands it back on expiry, /here brings it back,
// and a source that appears later takes over.
func TestPresenceNoSourceDeskDefault(t *testing.T) {
	idle := testkit.NewFakeIdle(0)
	idle.Unsupported()
	f := newPresence(t, idle)
	f.p.SetDeskDefault(true)
	ctx := context.Background()
	f.p.Poll(ctx)
	st := f.p.State()
	if !f.p.Quiet() || st.Supported || !st.ManualHere || st.Word() != "on" || idle.Calls() != 1 {
		t.Fatalf("desk default: quiet=%v state=%+v calls=%d", f.p.Quiet(), st, idle.Calls())
	}
	if v, ok := f.change(); !ok || !v {
		t.Fatalf("desk default change = %v, %v", v, ok)
	}
	f.p.Poll(ctx) // inside the retry window: not asked again
	if idle.Calls() != 1 || !f.p.Quiet() {
		t.Fatalf("retry window: calls=%d quiet=%v", idle.Calls(), f.p.Quiet())
	}

	// An open /away releases the default until /here, retries included.
	st = f.p.Away(0, 7)
	if st.Quiet || !st.ManualAway || st.Word() != "away-manual" {
		t.Errorf("/away without a source = %+v", st)
	}
	if v, ok := f.change(); !ok || v {
		t.Fatalf("change after /away = %v, %v", v, ok)
	}
	f.clock.Advance(time.Minute)
	f.p.Poll(ctx)
	if f.p.Quiet() || idle.Calls() != 2 {
		t.Fatalf("open /away after a retry: quiet=%v calls=%d", f.p.Quiet(), idle.Calls())
	}
	st = f.p.Here(7)
	if !st.ManualHere || !st.Quiet || st.Word() != "on" {
		t.Errorf("/here without a source = %+v", st)
	}
	if v, ok := f.change(); !ok || !v {
		t.Fatalf("change after /here = %v, %v", v, ok)
	}

	// A timed /away hands the default back the moment it expires, even
	// inside the retry window.
	f.p.Away(30*time.Second, 7)
	f.change()
	f.clock.Advance(30 * time.Second)
	f.p.Poll(ctx)
	if !f.p.Quiet() || f.p.State().ManualAway || idle.Calls() != 2 {
		t.Fatalf("timed /away expiry: quiet=%v state=%+v calls=%d", f.p.Quiet(), f.p.State(), idle.Calls())
	}
	if v, ok := f.change(); !ok || !v {
		t.Fatalf("change after expiry = %v, %v", v, ok)
	}

	// A source that appears takes over from presence by hand, and /here then
	// means automatic again.
	f.p.Here(7)
	idle.Set(time.Hour)
	f.p.Poll(ctx)
	if !f.p.Quiet() || f.p.State().Supported {
		t.Fatalf("source asked inside the retry window: %+v", f.p.State())
	}
	f.clock.Advance(time.Minute)
	f.p.Poll(ctx)
	st = f.p.State()
	if f.p.Quiet() || !st.Supported || st.ManualHere || st.Word() != "away" {
		t.Fatalf("source appeared while away: quiet=%v state=%+v", f.p.Quiet(), st)
	}
	if st = f.p.Here(7); st.Quiet || st.ManualHere {
		t.Fatalf("/here after the source appeared = %+v", st)
	}
	idle.Set(time.Second)
	f.p.Poll(ctx)
	if !f.p.Quiet() || f.p.State().ManualHere {
		t.Fatalf("automatic verdict at the desk: %+v", f.p.State())
	}
}

// TestPresenceNoSourceHeadless pins the headless case: no graphical
// session, so quiet stays off until /here asks for it by hand.
func TestPresenceNoSourceHeadless(t *testing.T) {
	idle := testkit.NewFakeIdle(0)
	idle.Unsupported()
	f := newPresence(t, idle)
	f.p.SetDeskDefault(false)
	ctx := context.Background()
	f.p.Poll(ctx)
	st := f.p.State()
	if f.p.Quiet() || st.Supported || st.ManualHere || st.Word() != "off" {
		t.Fatalf("headless without a source: quiet=%v state=%+v", f.p.Quiet(), st)
	}
	if _, ok := f.change(); ok {
		t.Error("headless without a source queued a change")
	}
	if st = f.p.Here(7); !st.Quiet || !st.ManualHere {
		t.Errorf("/here headless = %+v", st)
	}
	if st = f.p.Away(time.Minute, 7); st.Quiet || st.ManualHere {
		t.Errorf("/away headless = %+v", st)
	}
	// The expiry hands back the headless default: off.
	f.clock.Advance(time.Minute)
	f.p.Poll(ctx)
	if f.p.Quiet() || f.p.State().ManualAway {
		t.Errorf("timed /away expiry headless: %+v", f.p.State())
	}

	nilSource := app.NewPresence(nil, f.opts, f.clock, nil)
	nilSource.Poll(ctx)
	if nilSource.Quiet() || nilSource.State().Supported {
		t.Error("nil source should be unsupported")
	}
}

func TestPresenceFailing(t *testing.T) {
	ctx := context.Background()
	// A failure before any verdict is no verdict: quiet stays off, desk
	// default or not, since the source exists.
	idle := testkit.NewFakeIdle(0)
	idle.Fail(errors.New("gdbus: timeout"))
	first := newPresence(t, idle)
	first.p.SetDeskDefault(true)
	first.p.Poll(ctx)
	if first.p.Quiet() || !first.p.State().Supported || first.p.State().ManualHere {
		t.Fatalf("first sample failed: quiet=%v state=%+v", first.p.Quiet(), first.p.State())
	}

	// A transient failure keeps the previous verdict.
	g := newPresence(t, testkit.NewFakeIdle(time.Second))
	g.p.Poll(ctx)
	g.change()
	g.idle.Fail(errors.New("ioreg: exit 1"))
	g.p.Poll(ctx)
	if !g.p.Quiet() {
		t.Error("failure dropped the at-desk verdict")
	}
	g.idle.Set(time.Hour)
	g.p.Poll(ctx)
	if g.p.Quiet() {
		t.Error("recovered sample ignored")
	}
}

// blockingIdle answers Idle only once release is closed, after signalling
// entered.
type blockingIdle struct {
	entered chan struct{}
	release chan struct{}
	d       time.Duration
}

func (b *blockingIdle) Idle(ctx context.Context) (time.Duration, error) {
	close(b.entered)
	select {
	case <-b.release:
		return b.d, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func TestPresencePollProbesWithoutTheLock(t *testing.T) {
	f := newPresence(t, testkit.NewFakeIdle(0))
	idle := &blockingIdle{entered: make(chan struct{}), release: make(chan struct{}), d: time.Second}
	p := app.NewPresence(idle, f.opts, f.clock, nil)

	polled := make(chan struct{})
	go func() {
		defer close(polled)
		p.Poll(context.Background())
	}()
	<-idle.entered

	// While the probe hangs, the bridge goroutine's reads still answer.
	read := make(chan domain.PresenceState, 1)
	go func() {
		quiet := p.Quiet()
		st := p.State()
		st.Quiet = st.Quiet || quiet
		read <- st
	}()
	select {
	case st := <-read:
		if st.Quiet || st.AtDesk {
			t.Errorf("state before the probe answered = %+v", st)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Quiet/State blocked behind the idle probe")
	}

	close(idle.release)
	<-polled
	if !p.Quiet() || !p.State().AtDesk {
		t.Fatalf("probe result not applied: state = %+v", p.State())
	}
	select {
	case v := <-p.Changes():
		if !v {
			t.Fatal("change after the probe = false, want true")
		}
	default:
		t.Fatal("no change queued after the probe")
	}
}
