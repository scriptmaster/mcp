(() => {
  "use strict";
  const storageKeys = Object.freeze({
    apiKey: "mcp.prompt.apiKey",
    apiSecret: "mcp.prompt.apiSecret",
    systemPrompt: "mcp.prompt.systemPrompt"
  });
  const form = document.getElementById("prompt-form");
  const send = document.getElementById("send");
  const forget = document.getElementById("forget");
  const status = document.getElementById("status");
  const output = document.getElementById("response");
  const apiKey = document.getElementById("api-key");
  const apiSecret = document.getElementById("api-secret");
  const prompt = document.getElementById("prompt");
  const systemPrompt = document.getElementById("system-prompt");

  const setStatus = (message, isError) => {
    status.className = isError ? "meta error" : "meta";
    status.textContent = message;
  };

  const restoreSettings = () => {
    try {
      const savedKey = window.localStorage.getItem(storageKeys.apiKey);
      const savedSecret = window.localStorage.getItem(storageKeys.apiSecret);
      const savedSystemPrompt = window.localStorage.getItem(storageKeys.systemPrompt);
      if (savedKey !== null) apiKey.value = savedKey;
      if (savedSecret !== null) apiSecret.value = savedSecret;
      if (savedSystemPrompt !== null) systemPrompt.value = savedSystemPrompt;
      if (savedKey !== null || savedSecret !== null || savedSystemPrompt !== null) {
        setStatus("Ready · saved settings loaded.", false);
      }
    } catch (_error) {
      setStatus("Ready · browser storage is unavailable.", false);
    }
  };

  const saveCredentials = () => {
    try {
      window.localStorage.setItem(storageKeys.apiKey, apiKey.value.trim());
      window.localStorage.setItem(storageKeys.apiSecret, apiSecret.value);
      return true;
    } catch (_error) {
      return false;
    }
  };

  systemPrompt.addEventListener("input", () => {
    try {
      window.localStorage.setItem(storageKeys.systemPrompt, systemPrompt.value);
      setStatus("System prompt saved in this browser.", false);
    } catch (_error) {
      setStatus("System prompt changed, but browser storage is unavailable.", true);
    }
  });

  forget.addEventListener("click", () => {
    try {
      window.localStorage.removeItem(storageKeys.apiKey);
      window.localStorage.removeItem(storageKeys.apiSecret);
    } catch (_error) {
      // The fields are still cleared when browser storage is unavailable.
    }
    apiKey.value = "DevSpectra";
    apiSecret.value = "";
    setStatus("Saved credentials removed from this browser.", false);
  });

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    send.disabled = true;
    forget.disabled = true;
    setStatus("Calling POST /openai/prompt…", false);
    output.textContent = "Waiting for the AI provider…";
    const started = performance.now();
    try {
      const response = await fetch("/openai/prompt", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "X-API-Key": apiKey.value.trim(),
          "X-API-Secret": apiSecret.value
        },
        body: JSON.stringify({system_prompt: systemPrompt.value, prompt: prompt.value})
      });
      const data = await response.json().catch(() => ({error: "The server returned non-JSON data."}));
      if (!response.ok) throw new Error(data.error || "HTTP " + response.status);
      const saved = saveCredentials();
      output.textContent = data.text || "(The AI provider returned an empty text output.)";
      setStatus(
        "Success · " + (data.provider || "AI") + " · " + data.model + " · " +
        (data.usage?.total_tokens ?? 0) + " tokens · " +
        Math.round(performance.now() - started) + " ms browser round-trip" +
        (saved ? " · credentials saved" : " · credentials not saved"),
        false
      );
    } catch (error) {
      setStatus("Failed: " + error.message, true);
      output.textContent = "No successful response.";
    } finally {
      send.disabled = false;
      forget.disabled = false;
    }
  });

  restoreSettings();
})();
