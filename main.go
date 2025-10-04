package main

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/generative-ai-go/genai"
	"golang.org/x/net/html"
	"google.golang.org/api/option"
)

const (
	ModelName  = "gemini-2.5-flash-lite"
	InputPath  = "3e3522b1.epub"
	OutputPath = "learning_ebpf.epub"
)

func translateText(ctx context.Context, client *genai.Client, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", nil
	}

	prompt := fmt.Sprintf(
		"You are a professional translator. Translate the following text into natural and fluent Korean. Do not include any other explanations or supplementary text other than the translated text.\n\nOriginal Text:\n%s",
		text,
	)

	model := client.GenerativeModel(ModelName)
	resp, err := model.GenerateContent(
		ctx,
		genai.Text(prompt),
	)

	if err != nil {
		return "", fmt.Errorf("Gemini API call error: %w", err)
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		if t, ok := resp.Candidates[0].Content.Parts[0].(genai.Text); ok {
			return string(t), nil
		}
	}

	return "", fmt.Errorf("text not found in Gemini response or format is incorrect")
}

func processHTMLContent(ctx context.Context, client *genai.Client, htmlContent string) (string, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return htmlContent, fmt.Errorf("HTML parsing error: %w", err)
	}

	var textNodes []*html.Node
	var textsToTranslate []string

	var f func(*html.Node)
	f = func(n *html.Node) {

		if n.Type == html.ElementNode && n.Data == "p" {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				if c.Type == html.TextNode && strings.TrimSpace(c.Data) != "" {
					textNodes = append(textNodes, c)
					textsToTranslate = append(textsToTranslate, strings.TrimSpace(c.Data))
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	if len(textsToTranslate) == 0 {
		return htmlContent, nil
	}

	combinedText := strings.Join(textsToTranslate, "\n---\n")
	translatedCombinedText, err := translateText(ctx, client, combinedText)
	if err != nil {
		return htmlContent, err
	}

	translatedTexts := strings.Split(translatedCombinedText, "\n---\n")

	if len(translatedTexts) != len(textsToTranslate) {
		fmt.Printf("Warning: The number of original (%d) and translated (%d) text chunks do not match. Reverting to original.\n", len(textsToTranslate), len(translatedTexts))
		return htmlContent, nil
	}

	for i, node := range textNodes {

		originalText := node.Data
		leadingSpace := originalText[:len(originalText)-len(strings.TrimLeft(originalText, " \t\n\r"))]
		trailingSpace := originalText[len(strings.TrimRight(originalText, " \t\n\r")):]
		node.Data = leadingSpace + translatedTexts[i] + trailingSpace
	}

	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return htmlContent, fmt.Errorf("error rendering modified HTML: %w", err)
	}

	return buf.String(), nil
}

func main() {

	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: The GEMINI_API_KEY environment variable must be set.")
		os.Exit(1)
	}

	if _, err := os.Stat(InputPath); os.IsNotExist(err) {
		fmt.Printf("Error: Source EPUB file (%s) not found. Please prepare the file.\n", InputPath)
		os.Exit(1)
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		fmt.Printf("Error creating API client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	r, err := zip.OpenReader(InputPath)
	if err != nil {
		fmt.Printf("Error opening EPUB file: %v\n", err)
		os.Exit(1)
	}
	defer r.Close()

	newFile, err := os.Create(OutputPath)
	if err != nil {
		fmt.Printf("Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer newFile.Close()

	w := zip.NewWriter(newFile)
	defer w.Close()

	fmt.Printf("🚀 Analyzing and translating EPUB file (%s -> %s)...\n", InputPath, OutputPath)
	for _, file := range r.File {

		rc, err := file.Open()
		if err != nil {
			fmt.Printf("  ⚠️ Error opening file inside zip %s: %v\n", file.Name, err)
			continue
		}

		fw, err := w.Create(file.Name)
		if err != nil {
			fmt.Printf("  ⚠️ Error creating output zip entry %s: %v\n", file.Name, err)
			rc.Close()
			continue
		}

		if strings.HasSuffix(file.Name, ".html") || strings.HasSuffix(file.Name, ".xhtml") {
			fmt.Printf("  ⚙️ Processing for translation: %s\n", file.Name)

			contentBytes, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				fmt.Printf("  Error reading content: %v. Keeping original.\n", err)
				fw.Write(contentBytes)
				continue
			}

			originalHTML := string(contentBytes)
			translatedHTML, err := processHTMLContent(ctx, client, originalHTML)

			if err != nil {
				fmt.Printf("  Gemini processing error: %v. Keeping original.\n", err)
				fw.Write(contentBytes)
			} else {

				fw.Write([]byte(translatedHTML))
			}
		} else {

			if _, err := io.Copy(fw, rc); err != nil {
				fmt.Printf("  ⚠️ Error copying file %s: %v\n", file.Name, err)
			}
			rc.Close()
		}
	}

	fmt.Printf("\nTranslation complete. Translated EPUB file: %s\n", OutputPath)
}
