import { useCallback, useEffect, useRef, useState } from "react";
import { Readiness, type Incident, type Notification } from "./api";
import { api } from "./lib";

const NOTES_EVERY = 4000;
const INCIDENTS_EVERY = 6000;
const READY_EVERY = 15000;
const KEEP = 100;

/**
 * Лента уведомлений, открытые сбои и состояние хранилища. Все - опросом, как описано в docs/api.md.
 * onNew вызывается с новыми уведомлениями (initial = true для первой загрузки - их не показываем тостами).
 */
export function useFeed(onNew: (notes: Notification[], initial: boolean) => void) {
  const [notes, setNotes] = useState<Notification[]>([]);
  const [unread, setUnread] = useState(0);
  const [incidents, setIncidents] = useState<Incident[]>([]);
  const [degraded, setDegraded] = useState(false);
  const cursor = useRef(0);
  const loaded = useRef(false);
  const onNewRef = useRef(onNew);
  onNewRef.current = onNew;

  const refreshIncidents = useCallback(async () => {
    try {
      setIncidents(await api.incidents());
    } catch {
      /* опрос: временная ошибка не важна */
    }
  }, []);

  const pollNotes = useCallback(async () => {
    try {
      const res = await api.notifications(cursor.current);
      setUnread(res.unread);
      if (res.notifications.length === 0) {
        loaded.current = true;
        return;
      }
      // два опроса могли уйти с одним курсором (StrictMode, медленная сеть): дубли по id отбрасываем
      const fresh = res.notifications.filter((n) => n.id > cursor.current);
      if (fresh.length === 0) return;
      cursor.current = fresh[fresh.length - 1].id;
      setNotes((prev) => [...prev, ...fresh].slice(-KEEP));
      onNewRef.current(fresh, !loaded.current);
      loaded.current = true;
      if (res.notifications.some((n) => n.kind === "incident" || n.kind === "incident_resolved")) void refreshIncidents();
    } catch {
      /* ignore */
    }
  }, [refreshIncidents]);

  const pollReady = useCallback(async () => {
    try {
      const res = await fetch("/readyz");
      const data = Readiness.safeParse(await res.json());
      setDegraded(data.success && data.data.status === "degraded");
    } catch {
      /* ядро недоступно - это покажут ошибки запросов */
    }
  }, []);

  useEffect(() => {
    void pollNotes();
    void refreshIncidents();
    void pollReady();
    const a = setInterval(() => void pollNotes(), NOTES_EVERY);
    const b = setInterval(() => void refreshIncidents(), INCIDENTS_EVERY);
    const c = setInterval(() => void pollReady(), READY_EVERY);
    return () => {
      clearInterval(a);
      clearInterval(b);
      clearInterval(c);
    };
  }, [pollNotes, refreshIncidents, pollReady]);

  const markAllRead = useCallback(async () => {
    if (cursor.current === 0) return;
    try {
      await api.markRead(cursor.current);
      setUnread(0);
      setNotes((prev) => prev.map((n) => ({ ...n, read: true })));
    } catch {
      /* ignore */
    }
  }, []);

  const openIncidents = incidents.filter((i) => !i.resolved_at);
  return { notes, unread, markAllRead, incidents, openIncidents, refreshIncidents, degraded };
}

export type Feed = ReturnType<typeof useFeed>;
