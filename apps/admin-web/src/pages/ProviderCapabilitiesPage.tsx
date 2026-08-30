import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FileJson2, Plus, ShieldCheck } from 'lucide-react'
import { api, asPage, formatDate } from '../lib/api'
import { Button, DataTable, EmptyState, ErrorState, Form, Modal, Panel, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type Provider = { id: string; name: string; slug: string; enabled: boolean; commercial_status?: string }
type CapabilityDocument = {
  id: string; provider_id: string; provider_name: string; schema_version: string; document: Record<string, unknown>
  source_url?: string; source_sha256: string; status: string; fetched_at: string; created_at: string
}

const exampleDocument = `{
  "capabilities": ["chat", "tools", "json"],
  "models": [],
  "processing_regions": [],
  "pricing": { "currency": "USD", "source": "official" },
  "capacity": { "shared": true, "enterprise_projects": false },
  "compliance": { "zdr": false, "data_collection": "declared" }
}`

export function ProviderCapabilitiesPage() {
  const client = useQueryClient()
  const toast = useToast()
  const [open, setOpen] = useState(false)
  const [providerID, setProviderID] = useState('')
  const [schemaVersion, setSchemaVersion] = useState('relayedock.provider-capabilities.v1')
  const [sourceURL, setSourceURL] = useState('')
  const [document, setDocument] = useState(exampleDocument)
  const providers = useQuery({ queryKey: ['providers', 'capabilities'], queryFn: () => api<unknown>('/providers').then(asPage<Provider>) })
  const documents = useQuery({ queryKey: ['provider-capabilities'], queryFn: () => api<unknown>('/provider-capabilities').then(asPage<CapabilityDocument>) })
  const providerMap = useMemo(() => new Map((providers.data?.items || []).map((provider) => [provider.id, provider])), [providers.data?.items])
  const publish = useMutation({
    mutationFn: () => api('/provider-capabilities', { method: 'POST', body: JSON.stringify({ provider_id: providerID, schema_version: schemaVersion, source_url: sourceURL || undefined, document: JSON.parse(document) }) }),
    onSuccess: () => { setOpen(false); void client.invalidateQueries({ queryKey: ['provider-capabilities'] }); toast('Provider capability document published') },
  })

  if (providers.isLoading || documents.isLoading) return <div className="page-stack"><Panel><Skeleton rows={7} /></Panel></div>
  if (providers.isError || documents.isError) return <ErrorState error={providers.error || documents.error} onRetry={() => { void providers.refetch(); void documents.refetch() }} />
  const rows = documents.data?.items || []

  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><FileJson2 size={14} />PROVIDER SELF-DESCRIPTION</div><h1>Provider capabilities</h1><p>Publish append-only, SHA-256-bound declarations for capabilities, models, prices, capacity, regions, and compliance.</p></div><Button variant="primary" onClick={() => setOpen(true)}><Plus size={14} />Publish document</Button></div>
    <div className="security-card tenant-safety"><ShieldCheck size={20} /><div><strong>Declaration is not verification</strong><p>Capability documents are Provider/source declarations. Platform quality measurements and independent price evidence remain separate.</p></div><StatusBadge value="append only" /></div>
    <Panel title="Active declarations" description="Publishing a new document supersedes the active version without changing historical content.">
      {rows.length === 0 ? <EmptyState title="No capability documents" /> : <DataTable rows={rows} rowKey={(row) => row.id} columns={[
        { key: 'provider', label: 'Provider', render: (row) => <div className="primary-cell"><strong>{row.provider_name || providerMap.get(row.provider_id)?.name}</strong><code>{providerMap.get(row.provider_id)?.slug || row.provider_id}</code></div> },
        { key: 'schema', label: 'Schema', render: (row) => <code>{row.schema_version}</code> },
        { key: 'capabilities', label: 'Capabilities', render: (row) => Array.isArray(row.document.capabilities) ? row.document.capabilities.map(String).join(', ') || '—' : '—' },
        { key: 'regions', label: 'Processing regions', render: (row) => Array.isArray(row.document.processing_regions) ? row.document.processing_regions.map(String).join(', ') || '—' : '—' },
        { key: 'digest', label: 'SHA-256', render: (row) => <code>{row.source_sha256.slice(0, 16)}…</code> },
        { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
        { key: 'fetched', label: 'Published', render: (row) => formatDate(row.fetched_at || row.created_at) },
      ]} />}
    </Panel>
    <Modal open={open} onClose={() => setOpen(false)} wide title="Publish Provider capability document" description="The canonical JSON body is hashed by the server and cannot be edited after publication." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="publish-capability" pending={publish.isPending}>Publish</SubmitButton></>}>
      <Form id="publish-capability" className="form-grid" onSubmit={() => publish.mutateAsync()}>
        <label><span>Provider *</span><select required value={providerID} onChange={(event) => setProviderID(event.target.value)}><option value="">Select Provider</option>{(providers.data?.items || []).map((provider) => <option key={provider.id} value={provider.id}>{provider.name} ({provider.slug})</option>)}</select></label>
        <label><span>Schema version *</span><input required value={schemaVersion} onChange={(event) => setSchemaVersion(event.target.value)} /></label>
        <label className="full-span"><span>Official source URL</span><input type="url" value={sourceURL} onChange={(event) => setSourceURL(event.target.value)} placeholder="https://provider.example/docs" /></label>
        <label className="full-span"><span>Capability JSON *</span><textarea required rows={16} value={document} onChange={(event) => setDocument(event.target.value)} spellCheck={false} /></label>
        {publish.isError && <div className="form-error full-span">{publish.error instanceof Error ? publish.error.message : 'Unable to publish capability document.'}</div>}
      </Form>
    </Modal>
  </div>
}
