(() => {
  if (window.__huntsman) return;
  const realSetTimeout = window.setTimeout.bind(window);
  const realClearTimeout = window.clearTimeout.bind(window);
  const now = () => Date.now();
  // Longer timers are usually polling or analytics, not content the page is about to show.
  const shortTimerMs = 1000;
  // Requests open longer than this are usually long polling or streams, not content the page is waiting on.
  const longRequestMs = 3000;
  // A timer scheduled by a timer callback this many levels deep is treated as a background loop.
  const maxChain = 4;
  let timers = 0, chain = 0, lastActivity = now();
  const touch = () => { lastActivity = now(); };

  const wrapTimer = (set, clear, delayed) => {
    const live = new Set();
    window[set] = function (handler, delay, ...args) {
      if (typeof handler !== 'function') return original[set](handler, delay, ...args);
      const depth = chain + 1;
      const tracked = depth <= maxChain && !(delayed && (Number(delay) || 0) > shortTimerMs);
      if (tracked) { timers++; touch(); }
      const id = original[set](function (...callArgs) {
        if (live.delete(id)) { timers--; touch(); }
        chain = depth;
        try { return handler.apply(this, delayed ? args : callArgs); } finally { chain = 0; }
      }, delay);
      if (tracked) live.add(id);
      return id;
    };
    window[clear] = function (id) {
      if (live.delete(id)) { timers--; touch(); }
      return original[clear](id);
    };
  };
  const original = { setTimeout: realSetTimeout, clearTimeout: realClearTimeout };
  wrapTimer('setTimeout', 'clearTimeout', true);
  if (window.requestAnimationFrame && window.cancelAnimationFrame) {
    original.requestAnimationFrame = window.requestAnimationFrame.bind(window);
    original.cancelAnimationFrame = window.cancelAnimationFrame.bind(window);
    wrapTimer('requestAnimationFrame', 'cancelAnimationFrame', false);
  }

  const open = new Map();
  let nextRequest = 0;
  const track = () => {
    const id = nextRequest++;
    open.set(id, now()); touch();
    return () => { if (open.delete(id)) touch(); };
  };
  const shortRequests = () => {
    let count = 0;
    for (const started of open.values()) if (now() - started < longRequestMs) count++;
    return count;
  };
  // Routers prefetch the pages a document links to; those requests are not content for this page.
  let linkCount = -1, linked = new Set();
  const key = u => u.origin + u.pathname.replace(/\/$/, '');
  const isPrefetch = target => {
    try {
      const u = new URL(target && target.url || target, location.href);
      const links = document.links;
      if (links.length !== linkCount) {
        linkCount = links.length;
        linked = new Set();
        for (const a of links) { try { linked.add(key(new URL(a.href))); } catch (e) {} }
      }
      return key(u) !== key(location) && linked.has(key(u));
    } catch (e) { return false; }
  };
  const ignore = () => () => {};
  if (window.fetch) {
    const realFetch = window.fetch;
    window.fetch = function (...args) {
      const done = isPrefetch(args[0]) ? ignore() : track();
      try {
        return realFetch.apply(this, args).then(r => { done(); return r; }, e => { done(); throw e; });
      } catch (e) { done(); throw e; }
    };
  }
  if (window.XMLHttpRequest) {
    const xhrOpen = window.XMLHttpRequest.prototype.open;
    window.XMLHttpRequest.prototype.open = function (method, target, ...rest) {
      this.__huntsmanPrefetch = isPrefetch(target);
      return xhrOpen.call(this, method, target, ...rest);
    };
    const send = window.XMLHttpRequest.prototype.send;
    window.XMLHttpRequest.prototype.send = function (...args) {
      const done = this.__huntsmanPrefetch ? ignore() : track();
      this.addEventListener('loadend', done);
      try { return send.apply(this, args); } catch (e) { done(); throw e; }
    };
  }
  // Schedulers such as React's yield through postMessage; count each hop as activity.
  for (const proto of [window.MessagePort && window.MessagePort.prototype, window]) {
    if (proto && proto.postMessage) {
      const post = proto.postMessage;
      proto.postMessage = function (...args) { touch(); return post.apply(this, args); };
    }
  }
  try {
    new MutationObserver(touch)
      .observe(document, { childList: true, subtree: true, characterData: true });
  } catch (e) {}

  window.__huntsman = {
    // Resolves when the selector exists, or when no tracked work is left and nothing has happened for settleMs.
    ready(settleMs, selector) {
      return new Promise(resolve => {
        const check = () => {
          if (selector) {
            if (document.readyState !== 'loading' && document.querySelector(selector)) return resolve('selector');
          } else if (document.readyState === 'complete' && timers <= 0 && shortRequests() === 0 && now() - lastActivity >= settleMs) {
            return resolve('idle');
          }
          realSetTimeout(check, 5);
        };
        check();
      });
    },
  };
})();
