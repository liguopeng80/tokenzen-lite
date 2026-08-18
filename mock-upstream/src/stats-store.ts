import type {
  RequestLogEntry,
  AggregateStats,
  VerificationResult,
} from "./types.js";
import { config } from "./config.js";

let entries: RequestLogEntry[] = [];
let stats: AggregateStats = {
  total_requests: 0,
  total_prompt_tokens: 0,
  total_completion_tokens: 0,
  total_tokens: 0,
  by_model: {},
  by_endpoint: {},
};

export function recordRequest(entry: RequestLogEntry): void {
  if (entries.length >= config.maxRequestLogEntries) {
    // Remove oldest 10% to avoid unbounded growth
    const removeCount = Math.floor(config.maxRequestLogEntries * 0.1);
    entries = entries.slice(removeCount);
  }

  entries.push(entry);

  stats.total_requests++;
  stats.total_prompt_tokens += entry.prompt_tokens;
  stats.total_completion_tokens += entry.completion_tokens;
  stats.total_tokens += entry.total_tokens;
  stats.by_model[entry.model] = (stats.by_model[entry.model] || 0) + 1;
  stats.by_endpoint[entry.endpoint] =
    (stats.by_endpoint[entry.endpoint] || 0) + 1;
}

export function getStats(): AggregateStats & { verification: VerificationResult } {
  return { ...stats, verification: verifyTotals() };
}

export function getRequests(filters?: {
  limit?: number;
  offset?: number;
  model?: string;
}): { data: RequestLogEntry[]; total: number } {
  let filtered = entries;
  if (filters?.model) {
    filtered = filtered.filter((e) => e.model === filters.model);
  }
  const total = filtered.length;
  const offset = filters?.offset ?? 0;
  const limit = filters?.limit ?? 50;
  const data = filtered.slice(offset, offset + limit);
  return { data, total };
}

export function resetStats(): void {
  entries = [];
  stats = {
    total_requests: 0,
    total_prompt_tokens: 0,
    total_completion_tokens: 0,
    total_tokens: 0,
    by_model: {},
    by_endpoint: {},
  };
}

export function verifyTotals(): VerificationResult {
  let recomputedRequests = 0;
  let recomputedPrompt = 0;
  let recomputedCompletion = 0;
  let recomputedTotal = 0;

  for (const entry of entries) {
    recomputedRequests++;
    recomputedPrompt += entry.prompt_tokens;
    recomputedCompletion += entry.completion_tokens;
    recomputedTotal += entry.total_tokens;
  }

  const match =
    recomputedRequests === stats.total_requests &&
    recomputedPrompt === stats.total_prompt_tokens &&
    recomputedCompletion === stats.total_completion_tokens &&
    recomputedTotal === stats.total_tokens;

  const discrepancies: string[] = [];
  if (recomputedRequests !== stats.total_requests)
    discrepancies.push(
      `requests: stored=${stats.total_requests}, recomputed=${recomputedRequests}`
    );
  if (recomputedPrompt !== stats.total_prompt_tokens)
    discrepancies.push(
      `prompt_tokens: stored=${stats.total_prompt_tokens}, recomputed=${recomputedPrompt}`
    );
  if (recomputedCompletion !== stats.total_completion_tokens)
    discrepancies.push(
      `completion_tokens: stored=${stats.total_completion_tokens}, recomputed=${recomputedCompletion}`
    );
  if (recomputedTotal !== stats.total_tokens)
    discrepancies.push(
      `total_tokens: stored=${stats.total_tokens}, recomputed=${recomputedTotal}`
    );

  return {
    match,
    discrepancy: match ? null : discrepancies.join("; "),
    recomputed: {
      total_requests: recomputedRequests,
      total_prompt_tokens: recomputedPrompt,
      total_completion_tokens: recomputedCompletion,
      total_tokens: recomputedTotal,
    },
  };
}
