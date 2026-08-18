export const config = {
  port: parseInt(process.env.PORT || "4000", 10),

  // Simulated latency
  defaultLatencyMs: parseInt(process.env.DEFAULT_LATENCY_MS || "100", 10),

  // Probability of simulated error (0.0 - 1.0)
  errorRate: parseFloat(process.env.ERROR_RATE || "0"),

  // Default response text for chat completions
  defaultResponseText:
    process.env.DEFAULT_RESPONSE_TEXT ||
    "Hello! I am a mock AI assistant for testing billing. How can I help you today?",

  // Token estimation: chars per token
  tokenCharsPerToken: parseInt(process.env.TOKEN_CHARS_PER_TOKEN || "4", 10),
  tokenCharsPerTokenCjk: parseInt(
    process.env.TOKEN_CHARS_PER_TOKEN_CJK || "2",
    10
  ),

  // Embedding dimension
  defaultEmbeddingDim: parseInt(
    process.env.DEFAULT_EMBEDDING_DIM || "1536",
    10
  ),

  // Stats store limits
  maxRequestLogEntries: parseInt(
    process.env.MAX_REQUEST_LOG_ENTRIES || "10000",
    10
  ),
} as const;
