package ast

import "testing"

// The poolGuard contract: a pooled node is released at most once per
// Get-cycle. A second release — the aliased-inner-across-two-wrappers
// bug — is REFUSED (no re-zeroing, no re-pooling, no child re-walk) and
// counted in PoolDoubleReleaseCount. See the poolGuard doc in pool.go.

// TestDoubleReleaseAliasedInner pins the originating scenario: two
// wrapper statements sharing one aliased inner. The second wrapper's
// release must refuse the inner instead of re-pooling it under the first
// caller's feet.
func TestDoubleReleaseAliasedInner(t *testing.T) {
	ResetPoolDoubleReleaseCount()

	inner := GetSelectStatement()
	w1 := GetExplainStatement()
	w2 := GetExplainStatement()
	w1.Statement = inner
	w2.Statement = inner // the aliasing bug under test

	// Both wrappers torn down together — the realistic shape of the
	// filed hazard (e.g. two statements of one AST released in a loop).
	PutExplainStatement(w1) // releases inner legitimately
	PutExplainStatement(w2) // inner is aliased: must be refused
	if got := PoolDoubleReleaseCount(); got != 1 {
		t.Fatalf("after aliased release: counter = %d, want 1", got)
	}

	// The refusal must keep the pool sound: the inner sits in the pool
	// exactly once, so two Gets must return two distinct statements.
	a := GetSelectStatement()
	b := GetSelectStatement()
	if a == b {
		t.Fatal("pool handed the same statement to two callers — double-release corrupted the pool")
	}
	PutSelectStatement(a)
	PutSelectStatement(b)
}

// TestDoubleReleaseAfterReissueIsUndetectable pins the guard's DOCUMENTED
// limit, so that any future change to it is a deliberate decision: once
// the pool re-issues a node (Get re-arms the guard), a stale release by a
// previous owner is indistinguishable from the new owner's legitimate
// release — detection would require ownership tokens in the Put API.
// This is the ABA case; see the poolGuard doc. If this test ever fails
// because the counter INCREMENTS, the guard learned to detect re-issue
// races: update the doc and this test together.
func TestDoubleReleaseAfterReissueIsUndetectable(t *testing.T) {
	ResetPoolDoubleReleaseCount()

	inner := GetSelectStatement()
	w1 := GetExplainStatement()
	w2 := GetExplainStatement()
	w1.Statement = inner
	w2.Statement = inner

	PutExplainStatement(w1)
	reused := GetSelectStatement() // pool re-issues inner; guard re-armed
	if reused != inner {
		// sync.Pool re-issue is not guaranteed (P migration, GC victim
		// flush); without the re-issue the scenario under test does not
		// arise. Rare on a single goroutine.
		t.Skip("pool did not re-issue the inner; ABA scenario not constructed")
	}
	PutExplainStatement(w2) // stale release: NOT detectable

	if got := PoolDoubleReleaseCount(); got != 0 {
		t.Fatalf("counter = %d — the guard now detects re-issue races; update the poolGuard docs and this test", got)
	}
}

// TestDoubleReleaseSkipsChildren — refusing a wrapper's second release
// must not re-walk its children either: the children were released with
// the first release and may already be live under a new owner.
func TestDoubleReleaseSkipsChildren(t *testing.T) {
	ResetPoolDoubleReleaseCount()

	child := GetBinaryExpression()
	parent := GetCaseExpression()
	parent.Value = child

	PutCaseExpression(parent) // releases child too
	reusedChild := GetBinaryExpression()

	// parent.Value was nil'd by the first release; rebuild the aliasing
	// hazard explicitly: a stale copy of the parent pointer is released
	// again while its former child is live under a new owner.
	parent.Value = reusedChild // simulate stale alias retaining the child
	PutCaseExpression(parent)  // must be refused at the PARENT guard

	if got := PoolDoubleReleaseCount(); got != 1 {
		t.Fatalf("counter = %d, want 1 (parent refusal only)", got)
	}
	// reusedChild must not have been re-pooled by the refused release.
	if next := GetBinaryExpression(); next == reusedChild {
		t.Fatal("refused wrapper release still re-pooled its child")
	}
	PutExpression(reusedChild)
}

// TestDoubleReleaseViaExpressionDispatch — the same guard fires through
// PutExpression's iterative dispatch, which pools nodes inline.
func TestDoubleReleaseViaExpressionDispatch(t *testing.T) {
	ResetPoolDoubleReleaseCount()

	e := GetBinaryExpression()
	PutExpression(e)
	PutExpression(e) // double release through the dispatcher
	if got := PoolDoubleReleaseCount(); got != 1 {
		t.Fatalf("counter = %d, want 1", got)
	}

	// Mixed mechanisms share the guard: typed Put then dispatcher.
	f := GetFunctionCall()
	PutFunctionCall(f)
	PutExpression(f)
	if got := PoolDoubleReleaseCount(); got != 2 {
		t.Fatalf("counter = %d, want 2", got)
	}
}

// TestDoubleReleaseSequenceStatements — the sequence helpers zero the
// whole struct on release; the guard must survive the wipe (re-marked
// released inside the pool).
func TestDoubleReleaseSequenceStatements(t *testing.T) {
	ResetPoolDoubleReleaseCount()

	s := NewCreateSequenceStatement()
	ReleaseCreateSequenceStatement(s)
	ReleaseCreateSequenceStatement(s)
	if got := PoolDoubleReleaseCount(); got != 1 {
		t.Fatalf("counter = %d, want 1", got)
	}
}

// TestGuardLifecycle — fresh struct literals (never pooled) release
// fine; Get re-arms a previously released node; no false positives in
// the normal single-owner flow.
func TestGuardLifecycle(t *testing.T) {
	ResetPoolDoubleReleaseCount()

	// Fresh literal, never from a pool.
	PutSelectStatement(&SelectStatement{})

	// Get → Put → Get → Put: each cycle releases exactly once.
	for i := 0; i < 3; i++ {
		s := GetSelectStatement()
		PutSelectStatement(s)
	}

	// Wrapper round-trip with a fresh inner each time.
	for i := 0; i < 3; i++ {
		w := GetExplainStatement()
		w.Statement = GetSelectStatement()
		PutExplainStatement(w)
	}

	if got := PoolDoubleReleaseCount(); got != 0 {
		t.Fatalf("false positives: counter = %d, want 0", got)
	}
}

// TestDoubleReleaseAllDispatchedTypes — every pooled type the expression
// dispatcher pools inline must carry the guard; a missed embed makes the
// pre-switch releasable assertion fail silently (review H1: eight types
// shipped unguarded in the first cut).
func TestDoubleReleaseAllDispatchedTypes(t *testing.T) {
	nodes := []Expression{
		&ExistsExpression{},
		&AnyExpression{},
		&AllExpression{},
		&ListExpression{},
		&UnaryExpression{},
		&ExtractExpression{},
		&PositionExpression{},
		&SubstringExpression{},
		&UpdateExpression{},
		&Identifier{},
		&BinaryExpression{},
		&LiteralValue{},
		&FunctionCall{},
		&CaseExpression{},
		&BetweenExpression{},
		&InExpression{},
		&TupleExpression{},
		&ArrayConstructorExpression{},
		&SubqueryExpression{},
		&AliasedExpression{},
		&ArraySliceExpression{},
		&ArraySubscriptExpression{},
		&CastExpression{},
		&IntervalExpression{},
	}
	for _, n := range nodes {
		ResetPoolDoubleReleaseCount()
		PutExpression(n)
		PutExpression(n)
		if got := PoolDoubleReleaseCount(); got != 1 {
			t.Errorf("%T: counter = %d after double dispatch release, want 1", n, got)
		}
	}
}

// TestDoubleReleaseMixedUpdateExpression — the dispatcher now pools
// *UpdateExpression (it previously consumed the guard and dropped the
// node un-pooled, making a later typed Put false-count a leak).
func TestDoubleReleaseMixedUpdateExpression(t *testing.T) {
	ResetPoolDoubleReleaseCount()
	u := GetUpdateExpression()
	PutExpression(u)       // dispatch path pools it
	PutUpdateExpression(u) // typed path must refuse exactly once
	if got := PoolDoubleReleaseCount(); got != 1 {
		t.Fatalf("counter = %d, want 1", got)
	}

	// The dispatch case must actually POOL the node, not just consume
	// the guard and drop it (the original leak): the pool re-issues it.
	if reused := GetUpdateExpression(); reused != u {
		t.Fatal("dispatch release dropped the UpdateExpression instead of pooling it")
	}
}

// TestConcurrentDoubleReleaseDetected — the CAS guard is sound across
// goroutines: when two owners race to release the same aliased node,
// exactly one wins and the other is counted. A plain bool let both pass
// on an interleaved read (review M1). Run under -race.
func TestConcurrentDoubleReleaseDetected(t *testing.T) {
	const iterations = 200
	for i := 0; i < iterations; i++ {
		ResetPoolDoubleReleaseCount()
		s := GetSelectStatement()
		done := make(chan struct{}, 2)
		for j := 0; j < 2; j++ {
			go func() {
				PutSelectStatement(s)
				done <- struct{}{}
			}()
		}
		<-done
		<-done
		if got := PoolDoubleReleaseCount(); got != 1 {
			t.Fatalf("iteration %d: counter = %d, want exactly 1 (one winner, one refusal)", i, got)
		}
		// Drain the single pooled copy so iterations stay independent.
		_ = GetSelectStatement()
	}
}
