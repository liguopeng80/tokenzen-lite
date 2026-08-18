import { config } from "./config.js";

/**
 * Generate response text for a chat completion request.
 * Deterministic: same input always produces the same output.
 */
export function generateResponseText(messages: Array<{ role: string; content: string }>): string {
  return config.defaultResponseText;
}

/**
 * Split response text into SSE chunks.
 * Each chunk contains 3-5 words of the response text.
 * The final chunk (before [DONE]) includes the usage field.
 */
export function generateStreamChunks(
  id: string,
  model: string,
  text: string,
  usage: { prompt_tokens: number; completion_tokens: number; total_tokens: number },
  created: number
): string[] {
  const chunks: string[] = [];

  // First chunk: role announcement
  chunks.push(
    formatSSE({
      id,
      object: "chat.completion.chunk",
      created,
      model,
      choices: [
        { index: 0, delta: { role: "assistant" }, finish_reason: null },
      ],
    })
  );

  // Split text into word groups of 3-5 words
  const words = text.split(" ");
  const groupSize = 4;
  for (let i = 0; i < words.length; i += groupSize) {
    const fragment = words.slice(i, i + groupSize).join(" ");
    // Add space after fragment unless it's the last one
    const content =
      i + groupSize < words.length ? fragment + " " : fragment;

    chunks.push(
      formatSSE({
        id,
        object: "chat.completion.chunk",
        created,
        model,
        choices: [
          { index: 0, delta: { content }, finish_reason: null },
        ],
      })
    );
  }

  // Final chunk: finish reason + usage
  chunks.push(
    formatSSE({
      id,
      object: "chat.completion.chunk",
      created,
      model,
      choices: [{ index: 0, delta: {}, finish_reason: "stop" }],
      usage,
    })
  );

  // Done signal
  chunks.push("data: [DONE]\n\n");

  return chunks;
}

function formatSSE(data: object): string {
  return `data: ${JSON.stringify(data)}\n\n`;
}
