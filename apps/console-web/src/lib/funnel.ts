import { publicApi } from './public-api'

const anonymousKey = 'md-public-anonymous-id'
const visitKey = 'md-homepage-visit-event'

function identifier() {
  const existing = localStorage.getItem(anonymousKey)
  if (existing) return existing
  const created = crypto.randomUUID()
  localStorage.setItem(anonymousKey, created)
  return created
}

export function trackHomepageVisit() {
  const existing = sessionStorage.getItem(visitKey)
  if (existing) return
  const idempotencyKey = crypto.randomUUID()
  sessionStorage.setItem(visitKey, idempotencyKey)
  void publicApi('/funnel/events', {
    method: 'POST',
    headers: { 'Idempotency-Key': idempotencyKey },
    body: JSON.stringify({
      event_type: 'HOMEPAGE_VISITED',
      anonymous_id: identifier(),
      idempotency_key: idempotencyKey,
    }),
  }).catch(() => {
    sessionStorage.removeItem(visitKey)
  })
}
