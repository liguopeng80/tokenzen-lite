import { Router } from "express";
import { v4 as uuidv4 } from "uuid";
import { config } from "../config.js";
import { countInputTokens, countOutputTokens } from "../token-counter.js";
import { generateResponseText, generateStreamChunks } from "../response-generator.js";
import { recordRequest } from "../stats-store.js";
import type { ChatCompletionRequest } from "../types.js";

const router = Router();

router.post("/v1/chat/completions", async (req, res) => {
  const startTime = Date.now();
  const body = req.body as ChatCompletionRequest;
  const model = body.model || "gpt-4o-mini";
  const streaming = body.stream === true;
  const created = Math.floor(Date.now() / 1000);
  const id = `chatcmpl-${uuidv4().replace(/-/g, "").slice(0, 24)}`;

  // Simulate latency
  if (config.defaultLatencyMs > 0) {
    await sleep(config.defaultLatencyMs);
  }

  // Simulate error
  if (config.errorRate > 0 && Math.random() < config.errorRate) {
    res.status(500).json({
      error: {
        message: "Mock simulated error",
        type: "mock_error",
        code: "internal_error",
      },
    });
    return;
  }

  // Calculate tokens
  const promptTokens = countInputTokens(body.messages || []);
  const responseText = generateResponseText(body.messages || []);
  const completionTokens = countOutputTokens(responseText);
  const totalTokens = promptTokens + completionTokens;

  const usage = {
    prompt_tokens: promptTokens,
    completion_tokens: completionTokens,
    total_tokens: totalTokens,
  };

  const latencyMs = Date.now() - startTime;

  // Record stats
  recordRequest({
    id,
    timestamp: new Date().toISOString(),
    model,
    endpoint: "/v1/chat/completions",
    streaming,
    prompt_tokens: promptTokens,
    completion_tokens: completionTokens,
    total_tokens: totalTokens,
    latency_ms: latencyMs,
  });

  if (streaming) {
    // SSE streaming response
    res.setHeader("Content-Type", "text/event-stream");
    res.setHeader("Cache-Control", "no-cache");
    res.setHeader("Connection", "keep-alive");
    res.setHeader("X-Accel-Buffering", "no");

    const chunks = generateStreamChunks(id, model, responseText, usage, created);
    for (const chunk of chunks) {
      res.write(chunk);
    }
    res.end();
  } else {
    // Standard JSON response
    res.json({
      id,
      object: "chat.completion",
      created,
      model,
      choices: [
        {
          index: 0,
          message: { role: "assistant", content: responseText },
          finish_reason: "stop",
        },
      ],
      usage,
    });
  }
});

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export default router;
