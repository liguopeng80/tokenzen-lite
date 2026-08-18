import { config } from "./config.js";
import type { ChatMessage } from "./types.js";

const CJK_RANGES = /[\u4e00-\u9fff\u3400-\u4dbf\u3000-\u303f]/;

function estimateTokens(text: string): number {
  if (!text) return 0;
  let tokens = 0;
  for (const char of text) {
    tokens += CJK_RANGES.test(char)
      ? 1 / config.tokenCharsPerTokenCjk
      : 1 / config.tokenCharsPerToken;
  }
  return Math.max(1, Math.ceil(tokens));
}

export function countInputTokens(messages: ChatMessage[]): number {
  let total = 0;
  // ~4 tokens overhead per message (role, separators, etc.)
  for (const msg of messages) {
    total += estimateTokens(msg.content) + 4;
  }
  return total;
}

export function countOutputTokens(text: string): number {
  return estimateTokens(text);
}
