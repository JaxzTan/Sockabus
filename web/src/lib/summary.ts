"use client";
// AI summaries (GET/POST /internal/summary, /internal/personas/{id}/summary,
// /internal/conversations/{id}/summary) and the stage history (GET /internal/conversations/{id}/stages).
// Summaries are written by the Claude extract model on the Go side; the console only shows them.
// A missing or stale summary is regenerated once per mount (POST), never in a loop.
import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError } from "./api";
import { USE_SAMPLE } from "./store";

export interface ApiSummaryRow {
  id: number;
  summary: string;
  model: string;
  created_at: string;
}

interface SummaryEnvelope {
  stale: boolean;
  summary: ApiSummaryRow | null;
}

export interface SummaryState {
  summary: ApiSummaryRow | null;
  stale: boolean;
  loading: boolean; // generating or first load
  error: string | null;
  regenerate: () => void;
}

// useAISummary loads the newest summary at `path`. When there is none, or it is stale (new messages,
// new stage, older than 15 min), it asks for a fresh one once. `key` re-checks when the subject changes
// (e.g. a conversation's message count or stage). refreshStale=false only fills a missing summary and
// leaves a stale one to the backend's own refresh (every stage change / few messages) or the button.
export function useAISummary(path: string | null, key: string | number = "", refreshStale = true): SummaryState {
  const [summary, setSummary] = useState<ApiSummaryRow | null>(null);
  const [stale, setStale] = useState(false);
  const [loading, setLoading] = useState(!!path && !USE_SAMPLE);
  const [error, setError] = useState<string | null>(null);
  const busy = useRef(false);

  const generate = useCallback(async () => {
    if (!path || busy.current) return;
    busy.current = true;
    setLoading(true);
    try {
      const r = await api<SummaryEnvelope>(path, { method: "POST" });
      setSummary(r.summary);
      setStale(false);
      setError(null);
    } catch (e) {
      // 409: nothing to summarise yet (no messages); not an error worth showing in red.
      setError(e instanceof ApiError && e.status === 409 ? null : e instanceof Error ? e.message : "AI summary unavailable");
    } finally {
      busy.current = false;
      setLoading(false);
    }
  }, [path]);

  useEffect(() => {
    if (!path || USE_SAMPLE) return;
    let gone = false;
    api<SummaryEnvelope>(path)
      .then((r) => {
        if (gone) return;
        setSummary(r.summary);
        setStale(r.stale);
        setError(null);
        if (!r.summary || (r.stale && refreshStale)) void generate();
        else setLoading(false);
      })
      .catch((e) => {
        if (gone) return;
        setError(e instanceof Error ? e.message : "AI summary unavailable");
        setLoading(false);
      });
    return () => {
      gone = true;
    };
  }, [path, key, generate, refreshStale]);

  return { summary, stale, loading, error, regenerate: () => void generate() };
}

export interface ApiStageEvent {
  id: number;
  from_stage: string | null;
  to_stage: string;
  from_scam_type: string | null;
  scam_type: string | null;
  source: "bot" | "operator" | "classifier";
  created_at: string;
}

// useStageHistory: when each stage was first reached, keyed by stage ("rapport", "bait", ...).
export function useStageHistory(convId: string | null, key: string | number = ""): Record<string, ApiStageEvent> {
  const [reached, setReached] = useState<Record<string, ApiStageEvent>>({});
  useEffect(() => {
    if (!convId || USE_SAMPLE) return;
    let gone = false;
    api<{ events: ApiStageEvent[] }>(`/internal/conversations/${encodeURIComponent(convId)}/stages`)
      .then((r) => {
        if (gone) return;
        const out: Record<string, ApiStageEvent> = {};
        for (const e of r.events) if (!out[e.to_stage]) out[e.to_stage] = e;
        setReached(out);
      })
      .catch(() => {}); // the stage bar still shows the current stage without history
    return () => {
      gone = true;
    };
  }, [convId, key]);
  return reached;
}

export const summaryTime = (iso: string) => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toTimeString().slice(0, 5);
};
