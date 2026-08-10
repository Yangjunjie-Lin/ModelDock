-- RelayDock uses one audited OpenAI-compatible HTTP adapter for these
-- providers. Existing provider rows are never overwritten by this seed.
INSERT INTO providers (name, slug, provider_type, base_url, enabled, config)
VALUES
  ('DeepSeek', 'deepseek', 'deepseek', 'https://api.deepseek.com/v1', true,
   '{"compatibility":"openai","chat_completions":true}'::jsonb),
  ('OpenRouter', 'openrouter', 'openrouter', 'https://openrouter.ai/api/v1', true,
   '{"compatibility":"openai","chat_completions":true}'::jsonb)
ON CONFLICT (slug) DO NOTHING;
