package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/meganerd/electrictown/internal/rag"
)

// cmdRag implements the "et rag" subcommand group.
// Subcommands: ingest, query, stats.
func cmdRag(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: et rag <ingest|query|stats> [flags] [args]\n\nRun 'et rag <subcommand> --help' for details.")
	}
	switch args[0] {
	case "ingest":
		return cmdRagIngest(args[1:])
	case "query":
		return cmdRagQuery(args[1:])
	case "stats":
		return cmdRagStats(args[1:])
	default:
		return fmt.Errorf("unknown rag subcommand: %s\nUsage: et rag <ingest|query|stats>", args[0])
	}
}

// cmdRagIngest ingests a file or directory into the Qdrant collection.
func cmdRagIngest(args []string) error {
	fs := flag.NewFlagSet("rag ingest", flag.ExitOnError)
	ragURL := fs.String("rag-url", "http://ai01:6333", "Qdrant server URL")
	collection := fs.String("collection", "et-knowledge", "Qdrant collection name")
	embedURL := fs.String("embed-url", "http://ai01:11434", "Ollama URL for embeddings")
	embedModel := fs.String("embed-model", rag.DefaultEmbedModel, "Ollama embedding model name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: et rag ingest [flags] <path>")
	}
	path := fs.Arg(0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	client := rag.NewClient(*ragURL, *collection)
	embedder := rag.NewEmbedder(*embedURL, *embedModel)

	fmt.Printf("Ensuring collection '%s' at %s...\n", *collection, *ragURL)
	if err := client.EnsureCollection(ctx, rag.DefaultEmbedVectorSize); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}

	ingestor := rag.NewIngestor(client, embedder)

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	var total int
	if info.IsDir() {
		fmt.Printf("Ingesting directory: %s\n", path)
		total, err = ingestor.IngestDir(ctx, path)
	} else {
		fmt.Printf("Ingesting file: %s\n", path)
		total, err = ingestor.IngestFile(ctx, path)
	}
	if err != nil {
		return err
	}
	fmt.Printf("Ingested %d chunks into collection '%s'\n", total, *collection)
	return nil
}

// cmdRagQuery retrieves and prints the top-N most relevant document chunks.
func cmdRagQuery(args []string) error {
	fs := flag.NewFlagSet("rag query", flag.ExitOnError)
	ragURL := fs.String("rag-url", "http://ai01:6333", "Qdrant server URL")
	collection := fs.String("collection", "et-knowledge", "Qdrant collection name")
	embedURL := fs.String("embed-url", "http://ai01:11434", "Ollama URL for embeddings")
	embedModel := fs.String("embed-model", rag.DefaultEmbedModel, "Ollama embedding model name")
	limit := fs.Int("limit", 5, "number of results to return")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.Join(fs.Args(), " ")
	if query == "" {
		return fmt.Errorf("usage: et rag query [flags] <query text>")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client := rag.NewClient(*ragURL, *collection)
	embedder := rag.NewEmbedder(*embedURL, *embedModel)
	retriever := rag.NewRetriever(client, embedder)

	results, err := retriever.Retrieve(ctx, query, *limit)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("(no results found)")
		return nil
	}
	fmt.Print(retriever.FormatContext(results))
	return nil
}

// cmdRagStats prints basic statistics about the Qdrant collection.
func cmdRagStats(args []string) error {
	fs := flag.NewFlagSet("rag stats", flag.ExitOnError)
	ragURL := fs.String("rag-url", "http://ai01:6333", "Qdrant server URL")
	collection := fs.String("collection", "et-knowledge", "Qdrant collection name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := rag.NewClient(*ragURL, *collection)
	stats, err := client.Stats(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Collection: %s\n", *collection)
	fmt.Printf("URL:        %s\n", *ragURL)
	fmt.Printf("Status:     %s\n", stats.Status)
	fmt.Printf("Points:     %d\n", stats.PointsCount)
	return nil
}
