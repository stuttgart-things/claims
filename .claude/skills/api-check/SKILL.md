---
name: api-check
description: Verify claim-machinery API connection and list available templates
disable-model-invocation: true
---

Check if the claim-machinery API is running and accessible:

1. Determine the API URL from `$CLAIM_API_URL` environment variable, or use `http://localhost:8080` as default
2. Check API health by calling the `/templates` endpoint
3. List all available templates if the API is reachable
4. Report connection status clearly:
   - If successful: show available templates
   - If failed: show the error and suggest troubleshooting steps (is the API running? correct URL?)
