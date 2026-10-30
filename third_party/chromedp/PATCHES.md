# Patched chromedp v0.21.0

This is chromedp v0.21.0 without its tests, docs and agent files, plus one fix. `go.mod` at the repository root points `github.com/chromedp/chromedp` here with a `replace` directive. Drop this copy and the `replace` once an upstream release includes the fix ([chromedp/chromedp#1644](https://github.com/chromedp/chromedp/pull/1644)).

## `target.go`: clear the root node after invalidating it

`Target.documentUpdated` closes `f.Root.Invalidated`, then calls `DOM.getDocument`. When that call fails, for example with `context.Canceled` after the tab closes, it returns without replacing `f.Root`. The next `DOM.documentUpdated` event on the tab closes the same channel again and the process panics with `close of closed channel`.

That can happen because `Target.run` keeps draining queued DOM events after its context ends: Go's `select` picks at random among ready cases. Under heavy load, queued events pile up and the panic takes Huntsman down mid-run.

The fix sets `f.Root = nil` right after closing the channel. `ensureFrame` already treats a nil root as "not loaded yet".
