(() => {
  "use strict";
  const form = document.getElementById("prompt-form");
  const send = document.getElementById("send");
  const status = document.getElementById("status");
  const output = document.getElementById("response");

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    send.disabled = true;
    status.className = "meta";
    status.textContent = "Calling POST /openai/prompt…";
    output.textContent = "Waiting for the AI provider…";
    const started = performance.now();
    try {
      const response = await fetch("/openai/prompt", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-API-Key": document.getElementById("api-key").value,
          "X-API-Secret": document.getElementById("api-secret").value
        },
        body: JSON.stringify({prompt: document.getElementById("prompt").value})
      });
      const data = await response.json().catch(() => ({error: "The server returned non-JSON data."}));
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`);
      output.textContent = data.text || "(The AI provider returned an empty text output.)";
      status.textContent = `Success · ${data.provider || "AI"} · ${data.model} · ${data.usage?.total_tokens ?? 0} tokens · ${Math.round(performance.now()-started)} ms browser round-trip`;
    } catch (error) {
      status.className = "meta error";
      status.textContent = `Failed: ${error.message}`;
      output.textContent = "No successful response.";
    } finally {
      send.disabled = false;
    }
  });
})();
