// Port of internal/risk.Assess (keep the two in step): the rule-based score, reason and recommended
// action the lookup returns. Used where the browser builds a response itself (sample mode and the
// scripted bank story). Never recommends a freeze: the bank's reviewer decides.
import type { LookupResponse, RecommendedAction } from "./types";

export function assess(type: string, status: string, confidence: string, sightings: number): Required<Pick<LookupResponse, "risk_score" | "reason" | "signals" | "recommended_action">> {
  const base = confidence === "high" ? 0.9 : confidence === "medium" ? 0.7 : 0.5;
  const extra = sightings > 1 ? Math.min(0.02 * (sightings - 1), 0.08) : 0;
  const score = Math.round(Math.min(base + extra, 0.99) * 100) / 100;
  const reason = type === "phone" || type === "handle" ? "CONTACT_USED_BY_SCAMMER" : type === "url" ? "LINK_SHARED_BY_SCAMMER" : "ACCOUNT_SUPPLIED_BY_SCAMMER";
  const verified = status === "verified";
  const signals = [...(verified ? ["OPERATOR_VERIFIED"] : []), ...(sightings > 1 ? ["REPEAT_SIGHTINGS"] : [])];
  const action: RecommendedAction = verified && score >= 0.85 ? "HOLD_FOR_REVIEW" : score >= 0.6 ? "WARN_CUSTOMER" : "MONITOR";
  return { risk_score: score, reason, signals, recommended_action: action };
}

export const ACTION_LABEL: Record<RecommendedAction, string> = {
  HOLD_FOR_REVIEW: "Hold for review",
  WARN_CUSTOMER: "Warn customer",
  MONITOR: "Monitor",
};

export const REASON_LABEL: Record<string, string> = {
  ACCOUNT_SUPPLIED_BY_SCAMMER: "Account supplied by a scammer",
  CONTACT_USED_BY_SCAMMER: "Contact used by a scammer",
  LINK_SHARED_BY_SCAMMER: "Link shared by a scammer",
};
