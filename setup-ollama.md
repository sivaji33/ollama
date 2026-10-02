## Ollama Runtime Setup Instructions

To resolve the error:
> OwnBot backend returned HTTP 503: Required custom Ollama Codex runtime is unavailable at http://127.0.0.1:11435.
> The process serving 127.0.0.1:11435 could not be verified. Start the required executable `D:\ownbot\ollama-codex-runtime\bin\ollama.exe serve` and confirm that `qwen3:4b-instruct` is installed on that runtime.

### Steps to Fix:

1. **Start the Ollama Service**
   - Navigate to the directory: `D:\ownbot\ollama-codex-runtime\bin\`
   - Run the following command in your terminal:
     ```bash
     ollama.exe serve
     ```
   - This starts the Ollama server on `http://127.0.0.1:11435`.

2. **Install the `qwen3:4b-instruct` Model**
   - Run the following command in your terminal:
     ```bash
     ollama pull qwen3:4b-instruct
     ```
   - This downloads and installs the required model.

3. **Verify the Model is Available**
   - You can verify the model is installed by running:
     ```bash
     ollama list
     ```
   - Ensure `qwen3:4b-instruct` appears in the list.

4. **Restart the Application**
   - After completing the above steps, restart the OwnBot application to ensure it detects the runtime and model.

> ⚠️ Note: The Ollama service must be running and the model must be installed for the backend to function correctly.

---
This setup is required for the OwnBot backend to communicate with the Ollama Codex runtime.