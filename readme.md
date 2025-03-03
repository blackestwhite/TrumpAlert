# TrumpAlert

A tool that monitors Donald Trump's Truth Social posts, analyzes their potential impact on cryptocurrency markets, and sends the analysis to a Telegram channel.

## Features

- Automatically fetches Trump's posts from Truth Social
- Uses Gemini AI to analyze the potential impact on crypto markets
- Categorizes posts as positive, negative, or neutral for crypto
- Sends analysis to a Telegram channel
- Stores processed posts in Supabase to avoid duplicates

## Setup

1. Clone the repository
2. Install dependencies: `go mod tidy`
3. Create a `.env` file with:
SUPABASE_URL=your_supabase_url
SUPABASE_KEY=your_supabase_key
TELEGRAM_BOT_TOKEN=your_telegram_bot_token
TELEGRAM_CHANNEL_ID=@your_channel_name
GOOGLE_AI_STUDIO_API_KEY=your_gemini_api_key
4. Create a `processed_posts` table in Supabase
5. Run with: `go run main.go`

## Deployment

Can be deployed using GitHub Actions with a workflow that runs every 3 minutes.