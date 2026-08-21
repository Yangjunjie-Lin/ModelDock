import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Activity, Gauge, Link2, RefreshCw, RotateCcw, Save, ShieldAlert } from 'lucide-react'
import { api, asPage, formatDate, formatNumber } from '../lib/api'
import { Button, DataTable, EmptyState, ErrorState, Form, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type Policy = {
  provider_id: string; enabled: boolean; probe_model_id?: string; probe_interval_seconds: number; probe_timeout_ms: number
  evaluation_window_minutes: number; minimum_samples: number; availability_target_pct: string; maximum_error_rate_pct: string
  maximum_429_rate_pct: string; maximum_ttft_ms: number; maximum_full_latency_ms: number; minimum_throughput_tps: string
  minimum_output_quality_score: string; price_truth_tolerance_bps: number; required_test_regions: string[]
  auto_downweight_enabled: boolean; auto_circuit_breaker_enabled: boolean; circuit_failure_threshold: number
  circuit_recovery_threshold: number; circuit_open_seconds: number; ramp_enabled: boolean; ramp_initial_bps: number
  ramp_step_bps: number; ramp_step_interval_seconds: number; configuration_source: string
  updated_at?: string
}
type State = {
  grade: string; quality_score: string; routing_multiplier: string; traffic_cap_bps: number; circuit_state: string
  availability_pct: string; error_rate_pct: string; rate_limited_pct: string; p95_ttft_ms?: number; p95_full_latency_ms?: number
  throughput_tps?: string; output_quality_score: string; price_truth_score: string; region_coverage_pct: string
  measurement_count: number; last_probe_at?: string; last_evaluated_at?: string
}
type Summary = { provider_id: string; provider_name: string; provider_slug: string; supplier_id?: string; supplier_name?: string; policy: Policy; state: State }
type SLAEvent = { id: string; provider_id: string; provider_name: string; metric: string; severity: string; status: string; observed_value?: string; threshold_value?: string; started_at: string; resolved_at?: string }
type Verification = { id: string; provider_id: string; model_id: string; source_type: string; source_reference: string; result: string; maximum_deviation_bps?: number; observed_at: string }
type Model = { id: string; provider_id: string; display_name: string; provider_model_id: string }

const emptyVerification = { provider_id: '', model_id: '', source_type: 'OFFICIAL_DOCUMENT', source_reference: '', evidence_sha256: '', observed_input_token_cost: '0', observed_cached_input_token_cost: '0', observed_output_token_cost: '0', observed_request_fixed_cost: '0', currency: 'USD', unit: '1000000' }

export function ProviderQualityPage() {
  const client = useQueryClient()
  const toast = useToast()
  const summaries = useQuery({ queryKey: ['provider-quality'], queryFn: () => api<unknown>('/provider-quality').then(asPage<Summary>), refetchInterval: 30_000 })
  const sla = useQuery({ queryKey: ['provider-quality-sla'], queryFn: () => api<unknown>('/provider-quality/sla-events', { query: { limit: 100 } }).then(asPage<SLAEvent>), refetchInterval: 30_000 })
  const verifications = useQuery({ queryKey: ['provider-price-verifications'], queryFn: () => api<unknown>('/provider-quality/price-verifications', { query: { limit: 100 } }).then(asPage<Verification>) })
  const models = useQuery({ queryKey: ['models', 'quality'], queryFn: () => api<unknown>('/models').then(asPage<Model>) })
  const rows = useMemo(() => summaries.data?.items || [], [summaries.data?.items])
  const [selectedID, setSelectedID] = useState('')
  const selected = useMemo(() => rows.find((item) => item.provider_id === selectedID) || rows[0], [rows, selectedID])
  const [policy, setPolicy] = useState<Policy | null>(null)
  const [regions, setRegions] = useState('')
  const [supplierID, setSupplierID] = useState('')
  const [supplierReason, setSupplierReason] = useState('')
  const [circuitReason, setCircuitReason] = useState('')
  const [verification, setVerification] = useState(emptyVerification)

  useEffect(() => {
    if (!selected) return
    setSelectedID(selected.provider_id)
    setPolicy({ ...selected.policy })
    setRegions(selected.policy.required_test_regions.join(','))
    setSupplierID(selected.supplier_id || '')
    setVerification((current) => ({ ...current, provider_id: selected.provider_id, model_id: current.provider_id === selected.provider_id ? current.model_id : '' }))
  }, [selected])

  const refresh = () => { void client.invalidateQueries({ queryKey: ['provider-quality'] }); void client.invalidateQueries({ queryKey: ['provider-quality-sla'] }); void client.invalidateQueries({ queryKey: ['provider-price-verifications'] }) }
  const savePolicy = useMutation({ mutationFn: () => api(`/providers/${selected?.provider_id}/quality-policy`, { method: 'PUT', body: JSON.stringify({ ...policy, required_test_regions: regions.split(',').map((value) => value.trim().toUpperCase()).filter(Boolean) }) }), onSuccess: () => { toast('Quality policy saved'); refresh() } })
  const evaluate = useMutation({ mutationFn: () => api(`/providers/${selected?.provider_id}/quality/evaluate`, { method: 'POST' }), onSuccess: () => { toast('Platform evidence evaluated'); refresh() } })
  const resetCircuit = useMutation({ mutationFn: () => api(`/providers/${selected?.provider_id}/quality/circuit-reset`, { method: 'POST', body: JSON.stringify({ reason: circuitReason.trim() }) }), onSuccess: () => { setCircuitReason(''); toast('Circuit moved to half-open'); refresh() } })
  const linkSupplier = useMutation({ mutationFn: () => api(`/providers/${selected?.provider_id}/supplier-link`, { method: 'POST', body: JSON.stringify({ supplier_id: supplierID, reason: supplierReason.trim() }) }), onSuccess: () => { setSupplierReason(''); toast('Supplier linked with ramp controls'); refresh() } })
  const verifyPrice = useMutation({ mutationFn: () => api('/provider-quality/price-verifications', { method: 'POST', headers: { 'Idempotency-Key': `price-verification-${crypto.randomUUID()}` }, body: JSON.stringify({ ...verification, unit: Number(verification.unit), observed_at: new Date().toISOString() }) }), onSuccess: () => { toast('Independent price evidence recorded'); setVerification({ ...emptyVerification, provider_id: selected?.provider_id || '' }); refresh() } })

  if (summaries.isLoading) return <div className="page-stack"><Panel><Skeleton rows={8} /></Panel></div>
  if (summaries.isError) return <ErrorState error={summaries.error} onRetry={() => summaries.refetch()} />
  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><Gauge size={14} />PLATFORM-MEASURED QUALITY</div><h1>Provider quality</h1><p>Independent traffic and synthetic evidence drives grades, ramp limits, downweighting, SLA events, and circuit state. Supplier declarations are display-only.</p></div><Button onClick={refresh}><RefreshCw size={14} />Refresh</Button></div>
    {rows.length === 0 ? <EmptyState title="No Providers" /> : <div className="metric-grid compact-metrics">
      <Panel title="Measured Providers"><strong className="billing-total">{rows.filter((item) => item.policy.enabled).length}</strong></Panel>
      <Panel title="Open circuits"><strong className="billing-total">{rows.filter((item) => item.state.circuit_state === 'OPEN').length}</strong></Panel>
      <Panel title="Active SLA events"><strong className="billing-total">{(sla.data?.items || []).filter((item) => item.status === 'OPEN').length}</strong></Panel>
    </div>}
    {rows.length > 0 && <Panel title="Quality state" description="Select a Provider to inspect or change its operator-owned policy."><DataTable rows={rows} rowKey={(row) => row.provider_id} columns={[
      { key: 'provider', label: 'Provider', render: (row) => <button className="table-link" onClick={() => setSelectedID(row.provider_id)}><strong>{row.provider_name}</strong><small>{row.supplier_name ? `Supplier: ${row.supplier_name}` : 'Platform Provider'}</small></button> },
      { key: 'grade', label: 'Grade', render: (row) => <StatusBadge value={row.state.grade} /> },
      { key: 'availability', label: 'Availability', render: (row) => `${row.state.availability_pct}%` },
      { key: 'latency', label: 'TTFT / full', render: (row) => `${formatNumber(row.state.p95_ttft_ms, false)} / ${formatNumber(row.state.p95_full_latency_ms, false)} ms` },
      { key: 'rates', label: 'Error / 429', render: (row) => `${row.state.error_rate_pct}% / ${row.state.rate_limited_pct}%` },
      { key: 'routing', label: 'Weight / cap', render: (row) => `${row.state.routing_multiplier} / ${row.state.traffic_cap_bps} bps` },
      { key: 'circuit', label: 'Circuit', render: (row) => <StatusBadge value={row.state.circuit_state} /> },
    ]} /></Panel>}
    {selected && policy && <>
      <Panel title={`${selected.provider_name} evidence`} description={`Last probe ${formatDate(selected.state.last_probe_at)} / ${selected.state.measurement_count} immutable observations`}><div className="metric-grid panel-pad"><div className="metric"><span>Quality score</span><strong>{selected.state.quality_score}</strong></div><div className="metric"><span>Throughput</span><strong>{selected.state.throughput_tps || '—'} tok/s</strong></div><div className="metric"><span>Output quality</span><strong>{selected.state.output_quality_score}%</strong></div><div className="metric"><span>Price truth</span><strong>{selected.state.price_truth_score}%</strong></div><div className="metric"><span>Region coverage</span><strong>{selected.state.region_coverage_pct}%</strong></div></div><div className="form-grid panel-pad"><label className="full-span"><span>Half-open reset reason</span><input value={circuitReason} maxLength={500} onChange={(event) => setCircuitReason(event.target.value)} /></label><div className="header-actions"><Button onClick={() => evaluate.mutate()} disabled={evaluate.isPending}><Activity size={14} />Evaluate now</Button><Button onClick={() => resetCircuit.mutate()} disabled={resetCircuit.isPending || !circuitReason.trim()}><RotateCcw size={14} />Half-open reset</Button></div></div></Panel>
      <Panel title="Measurement and automation policy" description="Only administrators can mutate this policy; PLATFORM_ADMIN is enforced in PostgreSQL."><Form className="form-grid panel-pad" onSubmit={() => savePolicy.mutateAsync()}>
        <label><span>Enabled</span><input type="checkbox" checked={policy.enabled} onChange={(event) => setPolicy({ ...policy, enabled: event.target.checked })} /></label>
        <label><span>Probe model</span><select value={policy.probe_model_id || ''} onChange={(event) => setPolicy({ ...policy, probe_model_id: event.target.value || undefined })}><option value="">Health only</option>{(models.data?.items || []).filter((model) => model.provider_id === selected.provider_id).map((model) => <option value={model.id} key={model.id}>{model.display_name || model.provider_model_id}</option>)}</select></label>
        <NumberField label="Probe interval seconds" value={policy.probe_interval_seconds} onChange={(value) => setPolicy({ ...policy, probe_interval_seconds: value })} />
        <NumberField label="Evaluation window minutes" value={policy.evaluation_window_minutes} onChange={(value) => setPolicy({ ...policy, evaluation_window_minutes: value })} />
        <NumberField label="Minimum samples" value={policy.minimum_samples} onChange={(value) => setPolicy({ ...policy, minimum_samples: value })} />
        <TextField label="Availability target %" value={policy.availability_target_pct} onChange={(value) => setPolicy({ ...policy, availability_target_pct: value })} />
        <TextField label="Maximum error %" value={policy.maximum_error_rate_pct} onChange={(value) => setPolicy({ ...policy, maximum_error_rate_pct: value })} />
        <TextField label="Maximum 429 %" value={policy.maximum_429_rate_pct} onChange={(value) => setPolicy({ ...policy, maximum_429_rate_pct: value })} />
        <NumberField label="Maximum TTFT ms" value={policy.maximum_ttft_ms} onChange={(value) => setPolicy({ ...policy, maximum_ttft_ms: value })} />
        <NumberField label="Maximum full latency ms" value={policy.maximum_full_latency_ms} onChange={(value) => setPolicy({ ...policy, maximum_full_latency_ms: value })} />
        <TextField label="Minimum throughput tok/s" value={policy.minimum_throughput_tps} onChange={(value) => setPolicy({ ...policy, minimum_throughput_tps: value })} />
        <label><span>Required probe regions</span><input value={regions} placeholder="CN,SG" onChange={(event) => setRegions(event.target.value)} /></label>
        <NumberField label="Initial ramp bps" value={policy.ramp_initial_bps} onChange={(value) => setPolicy({ ...policy, ramp_initial_bps: value })} />
        <NumberField label="Ramp step bps" value={policy.ramp_step_bps} onChange={(value) => setPolicy({ ...policy, ramp_step_bps: value })} />
        <label><span>Automatic downweight</span><input type="checkbox" checked={policy.auto_downweight_enabled} onChange={(event) => setPolicy({ ...policy, auto_downweight_enabled: event.target.checked })} /></label>
        <label><span>Automatic circuit breaker</span><input type="checkbox" checked={policy.auto_circuit_breaker_enabled} onChange={(event) => setPolicy({ ...policy, auto_circuit_breaker_enabled: event.target.checked })} /></label>
        <SubmitButton pending={savePolicy.isPending}><Save size={14} />Save policy</SubmitButton>
      </Form></Panel>
      <Panel title="Approved supplier ramp" description="Linking does not approve a route; commercial, region, price, quality, and credential gates remain authoritative."><Form className="form-grid panel-pad" onSubmit={() => linkSupplier.mutateAsync()}><label><span>Approved supplier ID</span><input value={supplierID} onChange={(event) => setSupplierID(event.target.value)} /></label><label><span>Link reason</span><input required maxLength={500} value={supplierReason} onChange={(event) => setSupplierReason(event.target.value)} /></label><SubmitButton pending={linkSupplier.isPending} disabled={!supplierID || !supplierReason.trim()}><Link2 size={14} />Link and start ramp</SubmitButton></Form></Panel>
      <Panel title="Independent price verification" description="Record only evidence read from an official API/document or contract invoice. Exact decimal strings never cross a binary floating-point boundary."><Form className="form-grid panel-pad" onSubmit={() => verifyPrice.mutateAsync()}>
        <label><span>Model</span><select required value={verification.model_id} onChange={(event) => setVerification({ ...verification, model_id: event.target.value })}><option value="">Select model</option>{(models.data?.items || []).filter((model) => model.provider_id === selected.provider_id).map((model) => <option key={model.id} value={model.id}>{model.display_name || model.provider_model_id}</option>)}</select></label>
        <label><span>Evidence source</span><select value={verification.source_type} onChange={(event) => setVerification({ ...verification, source_type: event.target.value })}><option>OFFICIAL_DOCUMENT</option><option>OFFICIAL_API</option><option>CONTRACT_INVOICE</option></select></label>
        <label className="full-span"><span>Source reference</span><input required value={verification.source_reference} onChange={(event) => setVerification({ ...verification, source_reference: event.target.value })} /></label>
        <label className="full-span"><span>Evidence SHA-256</span><input required minLength={64} maxLength={64} value={verification.evidence_sha256} onChange={(event) => setVerification({ ...verification, evidence_sha256: event.target.value.toLowerCase() })} /></label>
        {(['observed_input_token_cost', 'observed_cached_input_token_cost', 'observed_output_token_cost', 'observed_request_fixed_cost'] as const).map((key) => <TextField key={key} label={key.replaceAll('_', ' ')} value={verification[key]} onChange={(value) => setVerification({ ...verification, [key]: value })} />)}
        <TextField label="Currency" value={verification.currency} onChange={(value) => setVerification({ ...verification, currency: value.toUpperCase() })} /><TextField label="Unit" value={verification.unit} onChange={(value) => setVerification({ ...verification, unit: value })} />
        <SubmitButton pending={verifyPrice.isPending} disabled={!verification.model_id || !verification.evidence_sha256}><ShieldAlert size={14} />Record verification</SubmitButton>
      </Form></Panel>
    </>}
    <Panel title="Provider SLA events" description="Events open and resolve transactionally with platform evaluation.">{sla.isLoading ? <Skeleton rows={5} /> : (sla.data?.items || []).length === 0 ? <EmptyState title="No Provider SLA events" /> : <DataTable rows={sla.data?.items || []} rowKey={(row) => row.id} columns={[
      { key: 'provider', label: 'Provider', render: (row) => row.provider_name }, { key: 'metric', label: 'Metric', render: (row) => row.metric }, { key: 'observed', label: 'Observed / threshold', render: (row) => `${row.observed_value || '—'} / ${row.threshold_value || '—'}` }, { key: 'severity', label: 'Severity', render: (row) => <StatusBadge value={row.severity} /> }, { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> }, { key: 'started', label: 'Started', render: (row) => formatDate(row.started_at) },
    ]} />}</Panel>
    <Panel title="Price verification history" description="Supplier-applied prices are not included here.">{verifications.isLoading ? <Skeleton rows={4} /> : (verifications.data?.items || []).length === 0 ? <EmptyState title="No independent price evidence" /> : <DataTable rows={verifications.data?.items || []} rowKey={(row) => row.id} columns={[
      { key: 'provider', label: 'Provider ID', render: (row) => <code>{row.provider_id}</code> }, { key: 'model', label: 'Model ID', render: (row) => <code>{row.model_id}</code> }, { key: 'source', label: 'Source', render: (row) => row.source_type }, { key: 'result', label: 'Result', render: (row) => <StatusBadge value={row.result} /> }, { key: 'deviation', label: 'Max deviation', render: (row) => row.maximum_deviation_bps === undefined ? '—' : `${row.maximum_deviation_bps} bps` }, { key: 'time', label: 'Observed', render: (row) => formatDate(row.observed_at) },
    ]} />}</Panel>
  </div>
}

function NumberField({ label, value, onChange }: { label: string; value: number; onChange: (value: number) => void }) { return <label><span>{label}</span><input required type="number" min={0} value={value} onChange={(event) => onChange(Number(event.target.value))} /></label> }
function TextField({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) { return <label><span>{label}</span><input required inputMode="decimal" value={value} onChange={(event) => onChange(event.target.value)} /></label> }
