import { useQuery } from '@tanstack/react-query'
import { Activity, FileCheck2, Gauge, MapPin } from 'lucide-react'
import { DataTable, EmptyState, ErrorState, Metric, Panel, Skeleton, StatusBadge } from '../components/ui'
import { formatDate, formatNumber } from '../lib/api'
import { publicApi } from '../lib/public-api'
import { usePublicSettings } from '../lib/public-settings'

type ProviderQuality = {
  provider_id: string
  provider_name: string
  provider_slug: string
  grade: string
  quality_score: string
  availability_pct: string
  error_rate_pct: string
  rate_limited_pct: string
  p95_ttft_ms?: number
  p95_full_latency_ms?: number
  throughput_tps?: string
  measurement_count: number
  region_coverage_pct: string
  last_evaluated_at?: string
  measurement_source: string
}

type CapabilityDocument = {
  id: string
  provider_id: string
  provider_name: string
  schema_version: string
  document: Record<string, unknown>
  source_url?: string
  source_sha256: string
  fetched_at: string
}

type PublicList<T> = { items: T[]; region: string; updated_at?: string }

function documentSummary(document: Record<string, unknown>) {
  const capabilities = Array.isArray(document.capabilities) ? document.capabilities.map(String) : []
  const models = Array.isArray(document.models) ? document.models.map(String) : []
  const regions = Array.isArray(document.processing_regions) ? document.processing_regions.map(String) : []
  return [capabilities.length ? `${capabilities.length} capabilities` : '', models.length ? `${models.length} models` : '', regions.length ? `${regions.length} regions` : ''].filter(Boolean).join(' · ') || 'Structured declaration available'
}

export function ProviderHealthPage() {
  const settings = usePublicSettings()
  const quality = useQuery({
    queryKey: ['public-provider-quality', settings.region],
    queryFn: () => publicApi<PublicList<ProviderQuality>>('/provider-quality', { query: { region: settings.region } }),
  })
  const capabilities = useQuery({
    queryKey: ['public-provider-capabilities', settings.region],
    queryFn: () => publicApi<PublicList<CapabilityDocument>>('/catalog/provider-capabilities', { query: { region: settings.region } }),
  })
  const measurements = (quality.data?.items || []).reduce((sum, item) => sum + item.measurement_count, 0)
  const evaluated = (quality.data?.items || []).filter((item) => item.last_evaluated_at).length

  return <div className="page-stack">
    <div className="page-header"><div><h1>Provider quality & capabilities</h1><p>Platform measurements and Provider declarations remain separate, attributable evidence sources.</p></div><StatusBadge value={`region ${settings.region}`} /></div>
    <div className="metric-grid">
      <Metric label="Measured Providers" value={evaluated} hint="Published only after minimum samples" icon={<Gauge size={16} />} />
      <Metric label="Quality observations" value={formatNumber(measurements)} hint="Platform-measured traffic and probes" icon={<Activity size={16} />} />
      <Metric label="Capability documents" value={capabilities.data?.items.length || 0} hint="SHA-256 bound, append-only versions" icon={<FileCheck2 size={16} />} />
    </div>
    <Panel title="Public quality evidence" description="Unavailable, commercially unapproved, or under-sampled Providers are omitted from this projection.">
      {quality.isLoading && <Skeleton rows={5} />}
      {quality.isError && <ErrorState error={quality.error} onRetry={() => void quality.refetch()} />}
      {quality.isSuccess && quality.data.items.length === 0 && <EmptyState title="No publishable quality evidence" description="The selected region has no Provider that has reached its minimum measurement threshold." />}
      {!!quality.data?.items.length && <DataTable rows={quality.data.items} rowKey={(row) => row.provider_id} columns={[
        { key: 'provider', label: 'Provider', render: (row) => <div className="primary-cell"><strong>{row.provider_name}</strong><small>{row.provider_slug}</small></div> },
        { key: 'grade', label: 'Grade', render: (row) => <StatusBadge value={row.grade} /> },
        { key: 'availability', label: 'Availability', render: (row) => `${row.availability_pct}%` },
        { key: 'errors', label: 'Error / 429', render: (row) => `${row.error_rate_pct}% / ${row.rate_limited_pct}%` },
        { key: 'latency', label: 'P95 TTFT / full', render: (row) => `${row.p95_ttft_ms ?? '—'} / ${row.p95_full_latency_ms ?? '—'} ms` },
        { key: 'throughput', label: 'Throughput', render: (row) => row.throughput_tps ? `${row.throughput_tps} tok/s` : '—' },
        { key: 'samples', label: 'Samples', render: (row) => formatNumber(row.measurement_count) },
        { key: 'updated', label: 'Evaluated', render: (row) => formatDate(row.last_evaluated_at) },
      ]} />}
    </Panel>
    <Panel title="Provider capability declarations" description="Declarations describe supported features, pricing/capacity metadata, processing regions, and compliance claims; they do not replace platform measurement.">
      {capabilities.isLoading && <Skeleton rows={4} />}
      {capabilities.isError && <ErrorState error={capabilities.error} onRetry={() => void capabilities.refetch()} />}
      {capabilities.isSuccess && capabilities.data.items.length === 0 && <EmptyState title="No active capability documents" />}
      {!!capabilities.data?.items.length && <DataTable rows={capabilities.data.items} rowKey={(row) => row.id} columns={[
        { key: 'provider', label: 'Provider', render: (row) => <div className="primary-cell"><strong>{row.provider_name}</strong><small>{row.provider_id}</small></div> },
        { key: 'schema', label: 'Schema', render: (row) => <code>{row.schema_version}</code> },
        { key: 'summary', label: 'Declaration', render: (row) => documentSummary(row.document) },
        { key: 'region', label: 'Region scope', render: (row) => <span><MapPin size={12} /> {Array.isArray(row.document.processing_regions) ? row.document.processing_regions.map(String).join(', ') : 'Not declared'}</span> },
        { key: 'digest', label: 'Evidence digest', render: (row) => <code>{row.source_sha256.slice(0, 12)}…</code> },
        { key: 'updated', label: 'Fetched', render: (row) => formatDate(row.fetched_at) },
      ]} />}
    </Panel>
  </div>
}
