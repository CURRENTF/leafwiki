# Research API

LeafWiki can expose an opt-in API for AI agents to create and update research
experiment records as normal wiki pages.

Enable it explicitly:

```bash
LEAFWIKI_ENABLE_RESEARCH_API=true \
LEAFWIKI_RESEARCH_API_TOKEN='replace-with-a-long-secret' \
LEAFWIKI_RESEARCH_API_PASSWORD='replace-with-a-long-password' \
LEAFWIKI_RESEARCH_GIT_AUTOCOMMIT=true \
./leafwiki --jwt-secret "$LEAFWIKI_JWT_SECRET" --admin-password "$LEAFWIKI_ADMIN_PASSWORD"
```

When `LEAFWIKI_RESEARCH_API_TOKEN` is set, agents authenticate with:

```http
Authorization: Bearer replace-with-a-long-secret
```

Agents may also use a research API password:

```http
X-Research-Password: replace-with-a-long-password
```

or HTTP Basic auth:

```bash
curl -u "research-agent:$LEAFWIKI_RESEARCH_API_PASSWORD" "$BASE_URL/api/research/experiments"
```

If no token or password is configured and auth is enabled, the API falls back
to normal LeafWiki login cookies plus CSRF, so browser-authenticated editors can
still call it. Public servers should configure a token or password.

## Record Layout

Experiment pages are created under:

```text
projects/<project>/experiments/<yyyy>/<mm>/<canonical-id>.md
```

The canonical ID is server-generated:

```text
<project>-<yyyymmdd>-<agent-slug>
```

If the same ID already exists with the same fingerprint, the existing record is
returned. If it exists with a different fingerprint, the server appends a suffix
such as `-02`.

Research metadata is stored in Markdown frontmatter using `research_*` keys.
LeafWiki's own `leafwiki_*` keys remain managed by the existing page system.

## Endpoints

Create an experiment:

```bash
curl -X POST "$BASE_URL/api/research/experiments" \
  -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "project": "DeltaKV",
    "title": "Qwen3 KVzip SCBench SCDQ ratio 0.2",
    "slugHint": "qwen3-kvzip-scdq-r02",
    "status": "queued",
    "goal": "Run the Qwen3 KVzip SCDQ baseline.",
    "command": "bash scripts/tmp/run.sh",
    "model": "Qwen3-4B-Instruct-2507",
    "method": "KVzip",
    "benchmark": "SCBench",
    "tags": ["scbench", "scdq"],
    "fingerprint": {
      "run_root": "/data2/haojitai/outputs/deltakv/run-a"
    }
  }'
```

Append an event:

```bash
curl -X POST "$BASE_URL/api/research/experiments/$ID/events" \
  -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Queue started",
    "type": "queue",
    "status": "running",
    "content": "GPU wait loop started.",
    "metrics": {"expected_rows": 500}
  }'
```

Update status:

```bash
curl -X PATCH "$BASE_URL/api/research/experiments/$ID/status" \
  -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status": "completed", "note": "All expected rows are present."}'
```

Record results:

```bash
curl -X POST "$BASE_URL/api/research/experiments/$ID/results" \
  -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "status": "completed",
    "content": "Overall-5 improved over the previous diagnostic run.",
    "metrics": {"overall_5": 41.236},
    "artifacts": [
      {"label": "result.json", "path": "/data2/haojitai/outputs/deltakv/run-a/result.json"}
    ]
  }'
```

List or fetch records:

```bash
curl -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  "$BASE_URL/api/research/experiments?project=DeltaKV&status=completed"

curl -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  "$BASE_URL/api/research/experiments/$ID"
```

Search documents before writing:

```bash
curl -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  "$BASE_URL/api/research/docs/search?q=SCBench%20SCDQ&project=DeltaKV&limit=10"
```

Search returns page metadata and short snippets only. Use `project` to scope to
`projects/<project>/...`, and `kind=page` or `kind=section` to restrict node
types. Read the Markdown body only after selecting a relevant path:

```bash
curl -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  "$BASE_URL/api/research/docs/read?path=projects/deltakv/experiments/2026/06/deltakv-20260624-qwen3-kvzip-scdq-r02"
```

Recent documents are useful when an agent needs a quick project-level context
window:

```bash
curl -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  "$BASE_URL/api/research/docs/recent?project=DeltaKV&kind=page&limit=20"
```

For an existing experiment, agents can ask for a compact context bundle:

```bash
curl -H "Authorization: Bearer $LEAFWIKI_RESEARCH_API_TOKEN" \
  "$BASE_URL/api/research/experiments/$ID/context?limit=10"
```

The bundle includes the experiment Markdown plus related and recent document
snippets. Agents should then call `/api/research/docs/read` for any full
Markdown documents they need.
