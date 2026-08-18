// ── Request types ──────────────────────────────────────────

export interface ChatMessage {
  role: "system" | "user" | "assistant" | "tool";
  content: string;
}

export interface ChatCompletionRequest {
  model: string;
  messages: ChatMessage[];
  stream?: boolean;
  stream_options?: { include_usage?: boolean };
  temperature?: number;
  max_tokens?: number;
  [key: string]: unknown;
}

export interface EmbeddingRequest {
  model: string;
  input: string | string[];
  [key: string]: unknown;
}

// ── Response types ─────────────────────────────────────────

export interface UsageInfo {
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
}

export interface ChatCompletionResponse {
  id: string;
  object: "chat.completion";
  created: number;
  model: string;
  choices: Array<{
    index: number;
    message: { role: "assistant"; content: string };
    finish_reason: "stop";
  }>;
  usage: UsageInfo;
}

export interface ChatCompletionStreamChunk {
  id: string;
  object: "chat.completion.chunk";
  created: number;
  model: string;
  choices: Array<{
    index: number;
    delta: { role?: "assistant"; content?: string };
    finish_reason: string | null;
  }>;
  usage?: UsageInfo;
}

export interface EmbeddingResponse {
  object: "list";
  data: Array<{
    object: "embedding";
    index: number;
    embedding: number[];
  }>;
  model: string;
  usage: { prompt_tokens: number; total_tokens: number };
}

// ── Stats types ────────────────────────────────────────────

export interface RequestLogEntry {
  id: string;
  timestamp: string;
  model: string;
  endpoint: string;
  streaming: boolean;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  latency_ms: number;
}

export interface AggregateStats {
  total_requests: number;
  total_prompt_tokens: number;
  total_completion_tokens: number;
  total_tokens: number;
  by_model: Record<string, number>;
  by_endpoint: Record<string, number>;
}

export interface VerificationResult {
  match: boolean;
  discrepancy: string | null;
  recomputed: {
    total_requests: number;
    total_prompt_tokens: number;
    total_completion_tokens: number;
    total_tokens: number;
  };
}
