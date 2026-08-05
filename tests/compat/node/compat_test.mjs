// Compatibility check: the unmodified official `openai` npm client against
// delos-gateway. Run through run.sh, which starts the mock upstream and the
// gateway and installs this package.
//
// Nothing here is Delos-specific except baseURL: that is the whole claim.

import OpenAI from "openai";

const GATEWAY = process.env.DELOS_GATEWAY_URL ?? "http://127.0.0.1:8099";
const MODEL = process.env.DELOS_COMPAT_MODEL ?? "delos-mock";
const API_KEY = process.env.DELOS_API_KEY ?? "delos-dev";

const failures = [];

function check(name, condition, detail = "") {
  if (condition) {
    console.log(`  ok   ${name}`);
  } else {
    console.log(`  FAIL ${name}: ${detail}`);
    failures.push(name);
  }
}

const client = new OpenAI({
  baseURL: `${GATEWAY}/v1`,
  apiKey: API_KEY,
  maxRetries: 0,
});

console.log(`openai-node -> ${GATEWAY}/v1`);

const models = await client.models.list();
const ids = models.data.map((m) => m.id);
check("models.list contains the served model", ids.includes(MODEL), `got ${JSON.stringify(ids)}`);

const completion = await client.chat.completions.create({
  model: MODEL,
  messages: [{ role: "user", content: "hello from node" }],
});
const content = completion.choices[0].message.content;
check(
  "chat.completions.create round-trips the prompt",
  content === "echo: hello from node",
  `got ${JSON.stringify(content)}`,
);
check(
  "chat.completions.create reports usage",
  (completion.usage?.total_tokens ?? 0) > 0,
  `got ${JSON.stringify(completion.usage)}`,
);
check(
  "chat.completions.create finish_reason is stop",
  completion.choices[0].finish_reason === "stop",
  `got ${completion.choices[0].finish_reason}`,
);

const stream = await client.chat.completions.create({
  model: MODEL,
  messages: [{ role: "user", content: "streamed node" }],
  stream: true,
});
const deltas = [];
const finishReasons = [];
for await (const event of stream) {
  const choice = event.choices?.[0];
  if (!choice) continue;
  if (choice.delta?.content) deltas.push(choice.delta.content);
  if (choice.finish_reason) finishReasons.push(choice.finish_reason);
}
check("streaming yields multiple deltas", deltas.length > 1, `got ${JSON.stringify(deltas)}`);
check(
  "streaming reassembles the completion",
  deltas.join("") === "echo: streamed node",
  `got ${JSON.stringify(deltas.join(""))}`,
);
check(
  "streaming terminates with a finish_reason",
  finishReasons.length === 1 && finishReasons[0] === "stop",
  `got ${JSON.stringify(finishReasons)}`,
);

const embeddings = await client.embeddings.create({ model: MODEL, input: "embed me" });
check(
  "embeddings.create returns a vector",
  embeddings.data.length === 1 && embeddings.data[0].embedding.length === 4,
  `got ${JSON.stringify(embeddings.data)}`,
);

if (failures.length > 0) {
  console.log(`\n${failures.length} compatibility check(s) failed: ${JSON.stringify(failures)}`);
  process.exit(1);
}
console.log("\nall node client compatibility checks passed");
