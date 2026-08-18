import express from "express";
import { config } from "./config.js";
import healthRouter from "./routes/health.js";
import chatRouter from "./routes/chat-completions.js";
import embeddingsRouter from "./routes/embeddings.js";
import statsRouter from "./routes/stats.js";

const app = express();

// Parse JSON bodies with generous limit for large prompts
app.use(express.json({ limit: "10mb" }));

// Mount routes
app.use(healthRouter);
app.use(chatRouter);
app.use(embeddingsRouter);
app.use(statsRouter);

// 404 catch-all
app.use((_req, res) => {
  res.status(404).json({
    error: { message: "Not found", type: "invalid_request_error" },
  });
});

app.listen(config.port, () => {
  console.log(`[mock-api] Listening on port ${config.port}`);
  console.log(`[mock-api] Latency: ${config.defaultLatencyMs}ms, Error rate: ${config.errorRate}`);
  console.log(`[mock-api] Endpoints:`);
  console.log(`[mock-api]   POST /v1/chat/completions`);
  console.log(`[mock-api]   POST /v1/embeddings`);
  console.log(`[mock-api]   GET  /health`);
  console.log(`[mock-api]   GET  /stats`);
  console.log(`[mock-api]   GET  /stats/requests`);
  console.log(`[mock-api]   DEL  /stats`);
});
