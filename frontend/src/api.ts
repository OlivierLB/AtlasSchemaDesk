import type { NewSession } from "./model";

export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code?: string,
  ) {
    super(message);
  }
}
let sessionId = "";
let initPromise: Promise<string> | undefined;
let channel: BroadcastChannel | undefined;
let releaseOwner: (() => void) | undefined;
const instance = crypto.randomUUID();
const storageKey = "atlas-session";

async function ownSession(id: string): Promise<boolean> {
  if (!navigator.locks) return true;
  return new Promise<boolean>((resolve, reject) => {
    void navigator.locks
      .request(`atlas-session:${id}`, { ifAvailable: true }, async (lock) => {
        if (!lock) {
          resolve(false);
          return;
        }
        await new Promise<void>((release) => {
          releaseOwner = release;
          resolve(true);
        });
      })
      .catch(reject);
  });
}

export async function request<T>(
  path: string,
  body?: unknown,
  session = true,
): Promise<T> {
  const response = await fetch(path, {
    method: body === undefined ? "GET" : "POST",
    cache: "no-store",
    headers: {
      ...(body === undefined
        ? {}
        : { "Content-Type": "application/json", "X-DB-Atlas": "1" }),
      ...(session && sessionId ? { "X-Atlas-Session": sessionId } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (response.status === 204) return undefined as T;
  const payload: unknown = await response.json();
  if (!response.ok) {
    const failure = payload as { error?: string; code?: string };
    if (failure.code === "session_expired")
      window.dispatchEvent(new Event("atlas-session-expired"));
    throw new ApiError(
      failure.error || `HTTP ${response.status}`,
      response.status,
      failure.code,
    );
  }
  return payload as T;
}

export function initializeSession(): Promise<string> {
  if (initPromise) return initPromise;
  initPromise = (async () => {
    let candidate = "";
    try {
      candidate = sessionStorage.getItem(storageKey) || "";
    } catch {
      /* private mode */
    }
    if (typeof BroadcastChannel !== "undefined") {
      channel = new BroadcastChannel("atlas-session-owners");
      let claimed = false;
      channel.onmessage = ({
        data,
      }: MessageEvent<{ kind: string; id: string; instance: string }>) => {
        if (data.instance === instance) return;
        if (data.kind === "probe" && data.id === sessionId && sessionId) {
          channel?.postMessage({ kind: "owned", id: sessionId, instance });
        }
        if (data.kind === "owned" && data.id === candidate) claimed = true;
      };
      if (candidate && !navigator.locks) {
        channel.postMessage({ kind: "probe", id: candidate, instance });
        await new Promise((resolve) => setTimeout(resolve, 200));
        if (claimed) candidate = "";
      }
    } else {
      // Without ownership discovery, isolate the new document rather than share a transaction.
      candidate = "";
    }
    // Without a browser-owned lock, absence of a broadcast reply cannot prove that
    // an older tab is gone (it may be suspended). Prefer a fresh, isolated session.
    if (!navigator.locks) candidate = "";
    if (candidate && !(await ownSession(candidate))) candidate = "";
    sessionId = candidate;
    if (candidate) {
      try {
        await request("/api/sessions/current");
        return sessionId;
      } catch (error) {
        releaseOwner?.();
        if (!(error instanceof ApiError) || error.code !== "session_expired")
          throw error;
      }
    }
    const created = await request<NewSession>("/api/sessions", {}, false);
    sessionId = created.id;
    await ownSession(sessionId);
    try {
      sessionStorage.setItem(storageKey, sessionId);
    } catch {
      /* private mode */
    }
    return sessionId;
  })();
  const pending = initPromise;
  void pending.catch(() => {
    if (initPromise === pending) initPromise = undefined;
    releaseOwner?.();
  });
  return pending;
}

export async function replaceSession() {
  sessionId = "";
  initPromise = undefined;
  channel?.close();
  releaseOwner?.();
  try {
    sessionStorage.removeItem(storageKey);
  } catch {
    /* private mode */
  }
  return initializeSession();
}
