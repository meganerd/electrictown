# RAG Integration in electrictown

Retrieval-Augmented Generation (RAG) lets local Ollama workers answer questions
about documentation they were not trained on — recent vendor install guides,
lab-specific playbooks, internal runbooks, and any other niche content.

## Why RAG?

Local models (qwen3-coder and similar) have hard training cutoffs. When workers
generate code or configuration for a technology that changed after their cutoff
(e.g., Postal v3's Docker-based install replacing the legacy Ruby bare-metal
setup), they produce outdated output even with explicit task instructions.

RAG solves this by:
1. Embedding your documentation into a local vector store (Qdrant)
2. At `et run` time, retrieving the most relevant doc chunks
3. Injecting those chunks into both the mayor's decompose prompt and each worker's
   system prompt — giving workers grounded context even if they've never seen the docs

## Infrastructure Setup

### 1. Start Qdrant on ai01

```bash
# Run Qdrant with persistent storage
docker run -d \
  --name qdrant \
  --restart always \
  -p 6333:6333 \
  -v /opt/qdrant-data:/qdrant/storage \
  qdrant/qdrant:latest

# Verify it's healthy
curl http://ai01:6333/healthz
```

### 2. Pull the embedding model on ai01

```bash
# nomic-embed-text: 768-dimensional, fast, good quality
ollama pull nomic-embed-text
```

Verify embedding works:
```bash
curl http://ai01:11434/api/embed \
  -d '{"model": "nomic-embed-text", "input": "test document"}'
```

The response will contain an `embeddings` array.

## Ingesting Documents

Use `et rag ingest` to load documents into Qdrant.

### Ingest a single file

```bash
et rag ingest \
  --rag-url http://ai01:6333 \
  --embed-url http://ai01:11434 \
  ~/src/_meganerd_roles/postal.yaml
```

### Ingest an entire playbook repo

```bash
et rag ingest \
  --rag-url http://ai01:6333 \
  --embed-url http://ai01:11434 \
  ~/src/_meganerd_roles/
```

Supported file types: `.md`, `.txt`, `.yaml`, `.yml`.
Hidden files and directories are skipped.

### Check collection stats

```bash
et rag stats --rag-url http://ai01:6333
```

### Test a query manually

```bash
et rag query \
  --rag-url http://ai01:6333 \
  --embed-url http://ai01:11434 \
  "how to install postal mail server"
```

## Using RAG in et run

Add `--rag-url` to any `et run` command:

```bash
et run \
  --config ~/electrictown.yaml \
  --rag-url http://ai01:6333 \
  "Write an Ansible playbook to install Postal on mail.example.com"
```

**What happens:**
- Phase 0: et embeds the task description, searches Qdrant for top-3 relevant chunks
- The retrieved chunks are injected into:
  - The mayor's decompose prompt (so it plans with accurate doc context)
  - Every worker's system prompt (so they generate from grounded knowledge)

### All RAG flags for et run

| Flag | Default | Description |
|------|---------|-------------|
| `--rag-url` | `""` (disabled) | Qdrant server URL. Empty = no RAG |
| `--rag-collection` | `et-knowledge` | Qdrant collection name |
| `--rag-embed-url` | `http://ai01:11434` | Ollama URL for query embeddings |

## Keeping the Knowledge Base Current

### Option 1: Git post-commit hook

Automatically re-ingest a repo whenever you commit to it:

```bash
# ~/.config/git/hooks/post-commit (global) or .git/hooks/post-commit per repo
#!/bin/bash
REPO_DIR=$(git rev-parse --show-toplevel)
et rag ingest \
  --rag-url http://ai01:6333 \
  --embed-url http://ai01:11434 \
  "$REPO_DIR" \
  2>/dev/null
```

Make it executable: `chmod +x .git/hooks/post-commit`

### Option 2: Cron job for doc directories

```cron
# Refresh playbooks nightly at 2am
0 2 * * * /home/user/go/bin/et rag ingest \
  --rag-url http://ai01:6333 \
  --embed-url http://ai01:11434 \
  /home/user/src/_meganerd_roles/ >> /var/log/et-rag-ingest.log 2>&1
```

### Option 3: Ingest et synthesis outputs

After a successful `et run --output-dir <dir>`, ingest the output to preserve
knowledge of what was generated:

```bash
et rag ingest --rag-url http://ai01:6333 --embed-url http://ai01:11434 <output-dir>
```

This creates a feedback loop: each successful et session contributes to future
context, letting local workers stand on the shoulders of previous runs.

## Architecture

```
et run --rag-url http://ai01:6333 "task"
         │
    Phase 0: RAG
         │
    ┌────▼────────────┐
    │  Qdrant (ai01)  │  <──── et rag ingest (docs, playbooks, outputs)
    │  port 6333      │
    └────┬────────────┘
         │  top-3 chunks
    ┌────▼────────────────────────────────┐
    │  Context injected into:             │
    │  - mayor Decompose prompt           │
    │  - each worker system prompt        │
    └────┬────────────────────────────────┘
         │
    Phase 1: Decompose (Gemini mayor)
    Phase 2: Workers (local qwen3-coder fleet)
    Phase 3: Synthesize
    ...
```

## Embedding details

- **Model**: `nomic-embed-text` (768 dimensions, Ollama)
- **Chunking**: 1000-char windows with 200-char overlap
- **IDs**: Deterministic SHA-256 UUID from `source:chunk_index`
  (safe to re-ingest; updated docs overwrite their existing vectors)
- **Context budget**: top-3 chunks × 800 chars max per chunk ≈ ~2400 chars
  injected into prompts

## Package structure

```
internal/rag/
  client.go   — Qdrant REST client (no external dependencies)
  embed.go    — Ollama /api/embed wrapper
  ingest.go   — Document chunking and upsert pipeline
  query.go    — Semantic retrieval and context formatting
  rag_test.go — Unit tests (all stdlib, no live services needed)
```
