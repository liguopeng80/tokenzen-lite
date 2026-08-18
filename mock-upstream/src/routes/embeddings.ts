import { Router } from "express";
import { v4 as uuidv4 } from "uuid";
import { config } from "../config.js";
import { countInputTokens } from "../token-counter.js";
import { recordRequest } from "../stats-store.js";
import type { EmbeddingRequest } from "../types.js";

const router = Router();

/**
 * Simple deterministic hash for generating consistent embedding vectors.
 * Same input always produces the same embedding.
 */
function hashString(str: string): number {
  let hash = 5381;
  for (let i = 0; i < str.length; i++) {
    hash = ((hash << 5) + hash + str.charCodeAt(i)) & 0xffffffff;
  }
  return hash;
}

function generateEmbedding(text: string, dim: number): number[] {
  const embedding: number[] = [];
  let seed = hashString(text);
  for (let i = 0; i < dim; i++) {
    // Simple PRNG seeded by input text
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    // Normalize to [-1, 1] range
    embedding.push((seed / 0x7fffffff) * 2 - 1);
  }
  return embedding;
}

router.post("/v1/embeddings", async (req, res) => {
  const startTime = Date.now();
  const body = req.body as EmbeddingRequest;
  const model = body.model || "text-embedding-ada-002";

  // Simulate latency
  if (config.defaultLatencyMs > 0) {
    await new Promise<void>((resolve) => setTimeout(resolve, config.defaultLatencyMs));
  }

  // Normalize input to array
  const inputs = Array.isArray(body.input) ? body.input : [body.input];

  // Calculate tokens
  const promptTokens = inputs.reduce(
    (sum, text) => sum + countInputTokens([{ role: "user", content: text }]) - 4,
    0
  );
  // Subtract the per-message overhead we don't want for raw text
  const adjustedTokens = Math.max(1, promptTokens);

  // Generate embeddings
  const data = inputs.map((text, index) => ({
    object: "embedding" as const,
    index,
    embedding: generateEmbedding(text, config.defaultEmbeddingDim),
  }));

  const latencyMs = Date.now() - startTime;
  const id = `emb-${uuidv4().replace(/-/g, "").slice(0, 16)}`;

  recordRequest({
    id,
    timestamp: new Date().toISOString(),
    model,
    endpoint: "/v1/embeddings",
    streaming: false,
    prompt_tokens: adjustedTokens,
    completion_tokens: 0,
    total_tokens: adjustedTokens,
    latency_ms: latencyMs,
  });

  res.json({
    object: "list",
    data,
    model,
    usage: {
      prompt_tokens: adjustedTokens,
      total_tokens: adjustedTokens,
    },
  });
});

export default router;
