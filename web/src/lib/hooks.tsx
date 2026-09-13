import { tr } from "./i18n";
import { createContext, useContext, useEffect, useMemo, useState } from "react";
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { api, authHeaders, createWriter, currentTenant, demo } from "./api";
import { consumeStream } from "./stream";
import type { Page, Session } from "./types";
export const SessionContext = createContext<Session | null>(null);
export function useSession() {
  return useContext(SessionContext)!;
}
export function usePermissions() {
  const { identity } = useSession();
  return {
    write: ["operator", "admin", "owner"].includes(identity.role),
    admin: ["admin", "owner"].includes(identity.role),
  };
}
export const useWriter = () => useMemo(createWriter, []);
export function useAPI<T>(path: string, enabled = true) {
  return useQuery({
    queryKey: [currentTenant(), path],
    queryFn: ({ signal }) => api<T>(path, { signal }),
    enabled,
  });
}
export function useList<T>(path: string) {
  const query = useInfiniteQuery({
    queryKey: [currentTenant(), path],
    initialPageParam: "",
    queryFn: ({ pageParam, signal }) =>
      api<Page<T>>(
        `${path}${path.includes("?") ? "&" : "?"}limit=30${pageParam ? `&cursor=${encodeURIComponent(pageParam)}` : ""}`,
        { signal },
      ),
    getNextPageParam: (page) => page.next_cursor || undefined,
  });
  const rows = [
    ...new Map(
      query.data?.pages
        .flatMap((page) => page.data || [])
        .map((row, index) => [
          (
            row as {
              id?: string;
            }
          ).id || index,
          row,
        ]),
    ).values(),
  ];
  return { ...query, rows };
}
export function useLive() {
  const client = useQueryClient();
  const [status, setStatus] = useState(demo ? "demo" : "connecting");
  useEffect(() => {
    if (demo) return;
    const controller = new AbortController();
    let reconnect: ReturnType<typeof setTimeout> | undefined;
    let refresh: ReturnType<typeof setTimeout> | undefined;
    let cursor = "";
    let failures = 0;
    const queueRefresh = () => {
      if (refresh) return;
      refresh = setTimeout(() => {
        refresh = undefined;
        void client.invalidateQueries({
          predicate: (q) =>
            q.queryKey[0] === currentTenant() && q.queryKey[1] !== "session",
        });
      }, 700);
    };
    async function connect() {
      try {
        const response = await fetch(
          `/v1/tenants/${encodeURIComponent(currentTenant())}/events/stream`,
          {
            credentials: "same-origin",
            headers: {
              ...authHeaders(),
              ...(cursor ? { "Last-Event-ID": cursor } : {}),
            },
            signal: controller.signal,
          },
        );
        if (response.status === 401 || response.status === 403) {
          setStatus("unauthorized");
          return;
        }
        if (!response.ok) {
          if (response.status === 400) cursor = "";
          throw new Error(tr("\u8FDE\u63A5\u5931\u8D25"));
        }
        failures = 0;
        setStatus("live");
        queueRefresh();
        await consumeStream(response, (frame) => {
          if (frame.id !== undefined) cursor = frame.id;
          if (frame.data) queueRefresh();
        });
      } catch {
        if (controller.signal.aborted) return;
        setStatus("reconnecting");
        reconnect = setTimeout(
          connect,
          Math.min(30000, 1500 * 2 ** failures++) + Math.random() * 500,
        );
      }
    }
    void connect();
    return () => {
      controller.abort();
      clearTimeout(reconnect);
      clearTimeout(refresh);
    };
  }, [client]);
  return status;
}
export function useClock() {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30000);
    return () => clearInterval(timer);
  }, []);
  return now;
}
