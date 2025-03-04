package main

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/google/generative-ai-go/genai"
	"github.com/joho/godotenv"
	"github.com/nedpals/supabase-go"
	"google.golang.org/api/option"
)

const (
	truthSocialAPI = "https://truthsocial.com/api/v1"
	trumpAccountID = "107780257626128497"
)

// Post structure to match the JSON response
type Post struct {
	ID               string    `json:"id"`
	CreatedAt        time.Time `json:"created_at"`
	Content          string    `json:"content"`
	URL              string    `json:"url"`
	RepliesCount     int       `json:"replies_count"`
	ReblogsCount     int       `json:"reblogs_count"`
	FavouritesCount  int       `json:"favourites_count"`
	MediaAttachments []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"media_attachments"`
	Analysis string `json:"analysis,omitempty"`
}

type ProcessedPost struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Analysis  string    `json:"analysis"`
}

func analyzePost(client *genai.Client, post Post) (string, error) {
	model := client.GenerativeModel("gemini-2.0-flash-exp")
	prompt := fmt.Sprintf(`تحلیل کن که این پست ترامپ در Truth Social چه تأثیری بر بازار رمزارز می‌تواند داشته باشد. آیا این پست برای بازار رمزارز مفید، مضر یا خنثی است؟ دلایل خود را توضیح دهید. پاسخ را به فارسی و بدون استفاده از مارک‌داون یا فرمت‌های خاص بنویسید چون قرار است در تلگرام نمایش داده شود. تحلیل شما نباید بیشتر از ۲ پاراگراف باشد.

متن پست: %s
تعامل: %d پاسخ، %d بازنشر، %d پسند
زمان انتشار: %s

تحلیل:`,
		post.Content, post.RepliesCount, post.ReblogsCount, post.FavouritesCount, post.CreatedAt)

	resp, err := model.GenerateContent(context.Background(), genai.Text(prompt))
	if err != nil {
		return "", err
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("no content generated")
	}

	// Get the text content from the response
	text, ok := resp.Candidates[0].Content.Parts[0].(genai.Text)
	if !ok {
		return "", fmt.Errorf("unexpected response format")
	}

	return string(text), nil
}

func isProcessed(supabase *supabase.Client, postID string) bool {
	var result []ProcessedPost
	err := supabase.DB.From("processed_posts").Select("*").Eq("id", postID).Execute(&result)
	if err != nil {
		log.Printf("Error checking processed status: %v", err)
		return false
	}
	return len(result) > 0
}

func markAsProcessed(supabase *supabase.Client, post Post, analysis string) error {
	processedPost := ProcessedPost{
		ID:        post.ID,
		CreatedAt: post.CreatedAt,
		Analysis:  analysis,
	}

	var result []ProcessedPost
	err := supabase.DB.From("processed_posts").Insert(processedPost).Execute(&result)
	if err != nil {
		return fmt.Errorf("error marking post as processed: %w", err)
	}
	return nil
}

func stripHTMLTags(input string) string {
	// Remove HTML tags
	re := regexp.MustCompile("<[^>]*>")
	text := re.ReplaceAllString(input, "")

	// Replace HTML entities
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")

	return strings.TrimSpace(text)
}

func sendToTelegram(bot *tgbotapi.BotAPI, channelID string, post Post, analysis string) error {
	cleanContent := stripHTMLTags(post.Content)
	message := fmt.Sprintf(`🔔 پست جدید ترامپ:
%s

📊 تحلیل تأثیر بر بازار رمزارز:
%s

🔗 لینک: 
%s

این مطلب توسط هوش مصنوعی بررسی شده است و توصیه مالی نیست.
`,
		cleanContent, analysis, post.URL)

	msg := tgbotapi.NewMessageToChannel(channelID, message)
	// Remove HTML parsing since we're sending plain text
	msg.ParseMode = ""

	_, err := bot.Send(msg)
	return err
}

func getTrumpPosts() ([]Post, error) {
	client := &http.Client{
		Transport: &http.Transport{
			// This enables automatic decompression
			DisableCompression: false,
		},
	}

	url := fmt.Sprintf("%s/accounts/%s/statuses", truthSocialAPI, trumpAccountID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set query parameters
	q := req.URL.Query()
	q.Add("exclude_replies", "true")
	q.Add("only_media", "false")
	q.Add("limit", "20")
	req.URL.RawQuery = q.Encode()

	// Set headers
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")
	// Explicitly request gzip encoding
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Host", "truthsocial.com")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned non-200 status code: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	// Read the response body
	var reader io.ReadCloser
	switch resp.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err = gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("error creating gzip reader: %w", err)
		}
		defer reader.Close()
	default:
		reader = resp.Body
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	// Debug: Print first 100 characters of response
	if len(body) > 0 {
		previewLen := 100
		if len(body) < previewLen {
			previewLen = len(body)
		}
		log.Printf("Response preview: %s", string(body[:previewLen]))
	}

	var posts []Post
	if err := json.Unmarshal(body, &posts); err != nil {
		return nil, fmt.Errorf("error unmarshaling JSON: %w, body starts with: %s", err, string(body[:min(100, len(body))]))
	}

	return posts, nil
}

// Helper function for min
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func main() {
	godotenv.Load()

	// Initialize Supabase client
	supabaseURL := os.Getenv("SUPABASE_URL")
	supabaseKey := os.Getenv("SUPABASE_KEY")
	supabase := supabase.CreateClient(supabaseURL, supabaseKey)

	// Initialize Telegram bot
	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Fatalf("Error initializing Telegram bot: %v", err)
	}
	channelID := os.Getenv("TELEGRAM_CHANNEL_ID")

	// Initialize Gemini client
	ctx := context.Background()
	geminiClient, err := genai.NewClient(ctx, option.WithAPIKey(os.Getenv("GOOGLE_AI_STUDIO_API_KEY")))
	if err != nil {
		log.Fatalf("Error initializing Gemini client: %v", err)
	}
	defer geminiClient.Close()

	log.Printf("Fetching Trump posts from %s/accounts/%s/statuses", truthSocialAPI, trumpAccountID)
	posts, err := getTrumpPosts()
	if err != nil {
		log.Printf("Error fetching posts: %v", err)
		return
	}

	log.Printf("Successfully fetched %d posts", len(posts))
	for _, post := range posts {
		if !isProcessed(supabase, post.ID) {
			cleanContent := stripHTMLTags(post.Content)

			// Skip empty content posts but mark them as processed
			if strings.TrimSpace(cleanContent) == "" {
				err = markAsProcessed(supabase, post, "پست فاقد محتوای متنی")
				if err != nil {
					log.Printf("Error marking empty post as processed: %v", err)
				}
				continue
			}

			analysis, err := analyzePost(geminiClient, post)
			if err != nil {
				log.Printf("Error analyzing post %s: %v", post.ID, err)
				continue
			}

			err = sendToTelegram(bot, channelID, post, analysis)
			if err != nil {
				log.Printf("Error sending to Telegram: %v", err)
				continue
			}

			err = markAsProcessed(supabase, post, analysis)
			if err != nil {
				log.Printf("Error marking post as processed: %v", err)
			}
		}
	}
}
