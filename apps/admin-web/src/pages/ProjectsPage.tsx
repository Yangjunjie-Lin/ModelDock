import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleDollarSign, FolderKanban, MoreHorizontal, Plus, Route, ShieldCheck, Trash2, UserPlus } from 'lucide-react'
import { api, asPage, decimalRatioPercent, formatDate, formatMoney, formatNumber, maximumDecimalString, percentStringToRatio } from '../lib/api'
import { type BudgetEvent, type BudgetPolicy, type BudgetUsage, type Project, type ProjectMember, type ProjectRouteGrant, useAdminTenantScope, v2Paths } from '../lib/v2'
import { TenantSelector } from '../components/TenantSelector'
import { Badge, Button, DataTable, type Column, EmptyState, ErrorState, Form, Modal, Panel, Segmented, Skeleton, StatusBadge, SubmitButton, useToast } from '../components/ui'

type GovernanceTab = 'members' | 'routes' | 'budget'
type ModelRoute = { id: string; alias?: string; upstream_model?: string; provider_name?: string; enabled?: boolean }

export function ProjectsPage() {
  const scope = useAdminTenantScope()
  const [createOpen, setCreateOpen] = useState(false)
  const [tab, setTab] = useState<GovernanceTab>('members')
  const [form, setForm] = useState({ name: '', slug: '', description: '' })
  const client = useQueryClient()
  const toast = useToast()
  const project = scope.project

  const create = useMutation({
    mutationFn: () => api<Project>(v2Paths.organizationProjects(scope.organizationID), { method: 'POST', body: JSON.stringify({ ...form, organization_id: scope.organizationID, status: 'ACTIVE' }) }),
    onSuccess: (value) => {
      setCreateOpen(false)
      setForm({ name: '', slug: '', description: '' })
      scope.setProjectID(value.id)
      void client.invalidateQueries({ queryKey: ['v2-projects', scope.organizationID] })
      toast('Project created')
    },
  })
  const updateStatus = useMutation({
    mutationFn: ({ project: value, status }: { project: Project; status: string }) => api<Project>(v2Paths.project(value.id), { method: 'PUT', body: JSON.stringify({ ...value, status }) }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['v2-projects', scope.organizationID] }); toast('Project status updated') },
  })
  const archive = useMutation({
    mutationFn: (projectID: string) => api(v2Paths.project(projectID), { method: 'DELETE' }),
    onSuccess: () => { scope.setProjectID(''); void client.invalidateQueries({ queryKey: ['v2-projects', scope.organizationID] }); toast('Project archived') },
  })

  const columns: Column<Project>[] = [
    { key: 'name', label: 'Project', render: (row) => <div className="primary-cell"><strong>{row.name}</strong><code>{row.slug}</code></div> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'description', label: 'Description', className: 'wide-cell', render: (row) => <span>{row.description || '—'}</span> },
    { key: 'created', label: 'Created', render: (row) => <span className="muted-cell">{formatDate(row.created_at)}</span> },
    { key: 'actions', label: '', className: 'action-cell', render: (row) => <Button size="sm" variant="ghost" onClick={() => scope.setProjectID(row.id)} aria-label={`Manage ${row.name}`}><MoreHorizontal size={15} /></Button> },
  ]

  return <div className="page-stack">
    <div className="page-header"><div><div className="eyebrow-row"><span className="live-dot" />V2 PROJECT GOVERNANCE</div><h1>Projects</h1><p>Isolate models, membership, budgets, usage, logs, API keys, and webhook events.</p></div><Button variant="primary" disabled={!scope.organizationID} onClick={() => setCreateOpen(true)}><Plus size={15} />Create project</Button></div>
    <TenantSelector organizations={scope.organizationRows} organizationID={scope.organizationID} onOrganizationChange={scope.setOrganizationID} />
    <section className="resource-panel">
      {scope.projects.isLoading && <div className="panel-pad"><Skeleton rows={6} /></div>}
      {scope.projects.isError && <div className="panel-pad"><ErrorState error={scope.projects.error} onRetry={() => scope.projects.refetch()} /></div>}
      {scope.projects.isSuccess && scope.projectRows.length === 0 && <EmptyState title="No projects in this organization" description="Create a project to grant models and issue isolated API keys." action={<Button variant="primary" disabled={!scope.organizationID} onClick={() => setCreateOpen(true)}><Plus size={15} />Create project</Button>} />}
      {scope.projectRows.length > 0 && <DataTable columns={columns} rows={scope.projectRows} rowKey={(row) => row.id} />}
    </section>

    {project && <section className="governance-shell"><header><div><span className="governance-icon"><FolderKanban size={17} /></span><div><h2>{project.name}</h2><p><code>{project.id}</code> · {project.description || 'No description'}</p></div></div><div className="header-actions"><StatusBadge value={project.status} /><Button size="sm" onClick={() => updateStatus.mutate({ project, status: project.status === 'ACTIVE' ? 'DISABLED' : 'ACTIVE' })}>{project.status === 'ACTIVE' ? 'Disable' : 'Enable'}</Button>{project.status !== 'ARCHIVED' && <Button size="sm" variant="danger" onClick={() => archive.mutate(project.id)}><Trash2 size={13} />Archive</Button>}</div></header><div className="governance-tabs"><Segmented value={tab} onChange={setTab} options={[{ value: 'members', label: 'Members' }, { value: 'routes', label: 'Route grants' }, { value: 'budget', label: 'Budget' }]} /></div>{tab === 'members' && <ProjectMembers projectID={project.id} />}{tab === 'routes' && <ProjectRoutes projectID={project.id} />}{tab === 'budget' && <ProjectBudget projectID={project.id} />}</section>}

    <Modal open={createOpen} onClose={() => setCreateOpen(false)} title="Create project" description={`Create an isolated project in ${scope.organization?.name || 'the selected organization'}.`} footer={<><Button onClick={() => setCreateOpen(false)}>Cancel</Button><SubmitButton form="create-project" pending={create.isPending}>Create project</SubmitButton></>}><Form id="create-project" className="form-grid" onSubmit={() => create.mutateAsync()}><label><span>Name *</span><input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder="Production API" /></label><label><span>Slug *</span><input required pattern="[a-z0-9][a-z0-9-]*" value={form.slug} onChange={(event) => setForm({ ...form, slug: event.target.value.toLowerCase().replace(/[^a-z0-9-]/g, '-') })} placeholder="production-api" /></label><label className="full-span"><span>Description</span><textarea rows={3} value={form.description} onChange={(event) => setForm({ ...form, description: event.target.value })} placeholder="Purpose and owning team" /></label>{create.isError && <div className="form-error full-span">{create.error instanceof Error ? create.error.message : 'Unable to create project.'}</div>}</Form></Modal>
  </div>
}

function ProjectMembers({ projectID }: { projectID: string }) {
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ user_id: '', role: 'DEVELOPER', status: 'ACTIVE' })
  const client = useQueryClient()
  const toast = useToast()
  const result = useQuery({ queryKey: ['v2-project-members', projectID], queryFn: () => api<unknown>(v2Paths.projectMembers(projectID)).then(asPage<ProjectMember>) })
  const save = useMutation({
    mutationFn: () => api(v2Paths.projectMember(projectID, form.user_id), { method: 'PUT', body: JSON.stringify({ role: form.role, status: form.status }) }),
    onSuccess: () => { setOpen(false); setForm({ user_id: '', role: 'DEVELOPER', status: 'ACTIVE' }); void client.invalidateQueries({ queryKey: ['v2-project-members', projectID] }); toast('Project membership saved') },
  })
  const remove = useMutation({ mutationFn: (userID: string) => api(v2Paths.projectMember(projectID, userID), { method: 'DELETE' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['v2-project-members', projectID] }); toast('Project member removed') } })
  const rows = result.data?.items || []
  const columns: Column<ProjectMember>[] = [
    { key: 'member', label: 'Member', render: (row) => <div className="primary-cell"><strong>{row.display_name || row.email || row.user_id}</strong><small>{row.email || row.user_id}</small></div> },
    { key: 'role', label: 'Role', render: (row) => <Badge tone="violet">{row.role}</Badge> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.status} /> },
    { key: 'created', label: 'Added', render: (row) => <span className="muted-cell">{formatDate(row.created_at)}</span> },
    { key: 'actions', label: '', className: 'action-cell', render: (row) => <Button size="sm" variant="ghost" onClick={() => remove.mutate(row.user_id)} aria-label="Remove member"><Trash2 size={13} /></Button> },
  ]
  return <div className="governance-content"><div className="section-heading"><div><strong>Project members</strong><small>Removing or disabling membership invalidates that user's project keys immediately.</small></div><Button onClick={() => setOpen(true)}><UserPlus size={14} />Add member</Button></div>{result.isLoading && <Skeleton rows={5} />}{result.isError && <ErrorState error={result.error} onRetry={() => result.refetch()} />}{result.isSuccess && rows.length === 0 && <EmptyState title="No project members" description="Add an active organization member to this project." />}{rows.length > 0 && <DataTable columns={columns} rows={rows} rowKey={(row) => row.user_id} />}
    <Modal open={open} onClose={() => setOpen(false)} title="Add project member" description="The user must have an active membership in the parent organization." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="project-member" pending={save.isPending}>Save member</SubmitButton></>}><Form id="project-member" className="form-grid" onSubmit={() => save.mutateAsync()}><label className="full-span"><span>User ID *</span><input required value={form.user_id} onChange={(event) => setForm({ ...form, user_id: event.target.value })} /></label><label><span>Role</span><select value={form.role} onChange={(event) => setForm({ ...form, role: event.target.value })}><option value="VIEWER">Viewer</option><option value="DEVELOPER">Developer</option><option value="ADMIN">Administrator</option></select></label><label><span>Status</span><select value={form.status} onChange={(event) => setForm({ ...form, status: event.target.value })}><option value="ACTIVE">Active</option><option value="DISABLED">Disabled</option></select></label>{save.isError && <div className="form-error full-span">{save.error instanceof Error ? save.error.message : 'Unable to save membership.'}</div>}</Form></Modal>
  </div>
}

function ProjectRoutes({ projectID }: { projectID: string }) {
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ model_route_id: '', required_tags: '', excluded_tags: '' })
  const client = useQueryClient()
  const toast = useToast()
  const grants = useQuery({ queryKey: ['v2-project-routes', projectID], queryFn: () => api<unknown>(v2Paths.projectRoutes(projectID)).then(asPage<ProjectRouteGrant>) })
  const available = useQuery({ queryKey: ['resource', '/model-routes', 'v2-grants'], queryFn: () => api<unknown>('/model-routes', { query: { limit: 200 } }).then(asPage<ModelRoute>) })
  const grant = useMutation({
    mutationFn: () => api(v2Paths.projectRoutes(projectID), { method: 'POST', body: JSON.stringify({ model_route_id: form.model_route_id, enabled: true, routing_config: { required_credential_tags: splitTags(form.required_tags), excluded_credential_tags: splitTags(form.excluded_tags) } }) }),
    onSuccess: () => { setOpen(false); setForm({ model_route_id: '', required_tags: '', excluded_tags: '' }); void client.invalidateQueries({ queryKey: ['v2-project-routes', projectID] }); toast('Project route grant saved') },
  })
  const remove = useMutation({ mutationFn: (routeID: string) => api(v2Paths.projectRoute(projectID, routeID), { method: 'DELETE' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['v2-project-routes', projectID] }); toast('Project route grant removed') } })
  const rows = grants.data?.items || []
  const columns: Column<ProjectRouteGrant>[] = [
    { key: 'alias', label: 'Model alias', render: (row) => <div className="route-alias"><Route size={14} /><code>{row.alias || row.model_route_id}</code></div> },
    { key: 'upstream', label: 'Upstream model', render: (row) => <span>{row.upstream_model || '—'}</span> },
    { key: 'required', label: 'Required tags', render: (row) => <TagList value={row.routing_config?.required_credential_tags} empty="Any credential" /> },
    { key: 'excluded', label: 'Excluded tags', render: (row) => <TagList value={row.routing_config?.excluded_credential_tags} empty="None" /> },
    { key: 'status', label: 'Status', render: (row) => <StatusBadge value={row.enabled ? 'enabled' : 'disabled'} /> },
    { key: 'actions', label: '', className: 'action-cell', render: (row) => <Button size="sm" variant="ghost" disabled={!row.id} onClick={() => row.id && remove.mutate(row.id)} aria-label="Remove route grant"><Trash2 size={13} /></Button> },
  ]
  return <div className="governance-content"><div className="section-heading"><div><strong>Project model routes</strong><small>Only granted aliases are visible to project keys. Optional tags constrain credential eligibility.</small></div><Button onClick={() => setOpen(true)}><Plus size={14} />Grant route</Button></div><div className="inline-note governance-note"><ShieldCheck size={14} />An ungranted alias returns model_not_found before any upstream request is made.</div>{grants.isLoading && <Skeleton rows={5} />}{grants.isError && <ErrorState error={grants.error} onRetry={() => grants.refetch()} />}{grants.isSuccess && rows.length === 0 && <EmptyState title="No routes granted" description="Project API keys cannot invoke models until at least one route is granted." />}{rows.length > 0 && <DataTable columns={columns} rows={rows} rowKey={(row) => String(row.id || row.model_route_id)} />}
    <Modal open={open} onClose={() => setOpen(false)} title="Grant model route" description="Grant one stable alias and optionally constrain its eligible credential tags." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="route-grant" pending={grant.isPending}>Grant route</SubmitButton></>} wide><Form id="route-grant" className="form-grid" onSubmit={() => grant.mutateAsync()}><label className="full-span"><span>Model route *</span><select required value={form.model_route_id} onChange={(event) => setForm({ ...form, model_route_id: event.target.value })}><option value="">Select route</option>{available.data?.items.filter((route) => route.enabled !== false).map((route) => <option value={route.id} key={route.id}>{route.alias || route.id} · {route.upstream_model || 'configured model'}</option>)}</select></label><label><span>Required credential tags</span><input value={form.required_tags} onChange={(event) => setForm({ ...form, required_tags: event.target.value })} placeholder="region:apac, tier:production" /><small>Every listed tag must be present.</small></label><label><span>Excluded credential tags</span><input value={form.excluded_tags} onChange={(event) => setForm({ ...form, excluded_tags: event.target.value })} placeholder="maintenance, deprecated" /><small>Any matching tag makes a credential ineligible.</small></label>{available.isError && <div className="form-error full-span">{available.error instanceof Error ? available.error.message : 'Unable to load model routes.'}</div>}{grant.isError && <div className="form-error full-span">{grant.error instanceof Error ? grant.error.message : 'Unable to grant route.'}</div>}</Form></Modal>
  </div>
}

function ProjectBudget({ projectID }: { projectID: string }) {
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState({ name: 'Monthly project budget', token_limit: '', cost_limit: '', alert_threshold: '80', enforcement: 'WARN' })
  const client = useQueryClient()
  const toast = useToast()
  const policies = useQuery({ queryKey: ['v2-project-budgets', projectID], queryFn: () => api<unknown>(v2Paths.projectBudgets(projectID)).then(asPage<BudgetPolicy>) })
  const usage = useQuery({ queryKey: ['v2-project-budget-usage', projectID], queryFn: () => api<BudgetUsage>(v2Paths.projectBudgetUsage(projectID), { query: { period: 'MONTHLY' } }) })
  const events = useQuery({ queryKey: ['v2-project-budget-events', projectID], queryFn: () => api<unknown>(v2Paths.projectBudgetEvents(projectID), { query: { limit: 20 } }).then(asPage<BudgetEvent>) })
  const save = useMutation({
    mutationFn: () => api(v2Paths.projectBudgets(projectID), { method: 'POST', body: JSON.stringify({ name: form.name, period: 'MONTHLY', token_limit: form.token_limit ? Number(form.token_limit) : null, cost_limit: form.cost_limit || null, alert_threshold: percentStringToRatio(form.alert_threshold), enforce_hard_limit: form.enforcement === 'BLOCK', status: 'ACTIVE' }) }),
    onSuccess: () => { setOpen(false); void client.invalidateQueries({ queryKey: ['v2-project-budgets', projectID] }); toast('Project budget policy saved') },
  })
  const remove = useMutation({ mutationFn: (policyID: string) => api(v2Paths.projectBudget(projectID, policyID), { method: 'DELETE' }), onSuccess: () => { void client.invalidateQueries({ queryKey: ['v2-project-budgets', projectID] }); toast('Budget policy removed') } })
  const policyRows = policies.data?.items || []
  const usageData = usage.data || {}
  const tokenLimit = Math.max(...policyRows.map((policy) => Number(policy.token_limit || 0)), 0)
  const costLimit = maximumDecimalString(policyRows.map((policy) => policy.cost_limit || '0'))
  return <div className="governance-content"><div className="section-heading"><div><strong>Monthly project budget</strong><small>WARN emits a deduplicated event; BLOCK rejects before upstream dispatch.</small></div><Button onClick={() => setOpen(true)}><CircleDollarSign size={14} />Add policy</Button></div><div className="budget-summary"><BudgetMeter label="Tokens" value={String(usageData.total_tokens || 0)} limit={String(tokenLimit)} format={(value) => formatNumber(value)} /><BudgetMeter label="Estimated cost" value={usageData.cost || '0'} limit={costLimit} format={formatMoney} /><div><span>Requests</span><strong>{formatNumber(usageData.requests)}</strong><small>{formatNumber(usageData.errors, false)} errors this period</small></div></div>
    {policies.isLoading && <Skeleton rows={4} />}{policies.isError && <ErrorState error={policies.error} onRetry={() => policies.refetch()} />}{policies.isSuccess && policyRows.length === 0 && <EmptyState title="No project budget policy" description="Traffic is admitted using key and user limits only." />}{policyRows.map((policy) => <article className="policy-row" key={policy.id}><div><strong>{policy.name}</strong><small>{policy.period} · warning at {(Number(policy.alert_threshold || 0) * 100).toFixed(0)}%</small></div><div className="policy-limits"><span>{policy.token_limit ? `${formatNumber(policy.token_limit)} tokens` : 'No token limit'}</span><span>{policy.cost_limit ? `${formatMoney(policy.cost_limit)} cost` : 'No cost limit'}</span></div><Badge tone={policy.enforce_hard_limit ? 'danger' : 'warning'}>{policy.enforce_hard_limit ? 'BLOCK' : 'WARN'}</Badge><StatusBadge value={policy.status} /><Button size="sm" variant="ghost" onClick={() => remove.mutate(policy.id)} aria-label="Remove budget policy"><Trash2 size={13} /></Button></article>)}
    <Panel className="budget-events" title="Budget events" description="Deduplicated warning and exceeded events for this project">{events.isLoading && <Skeleton rows={3} />}{events.isError && <ErrorState error={events.error} onRetry={() => events.refetch()} />}{events.data?.items.length === 0 && <EmptyState title="No budget events" />}{events.data?.items.map((event) => <div className="event-row" key={event.id}><StatusBadge value={event.event_type} /><div><strong>{event.event_type.replaceAll('_', ' ')}</strong><small>{formatDate(event.created_at)} · {formatNumber(event.tokens)} tokens · {formatMoney(event.cost)}</small></div>{event.request_id && <code>{event.request_id}</code>}</div>)}</Panel>
    <Modal open={open} onClose={() => setOpen(false)} title="Add budget policy" description="At least one token or cost limit is required." footer={<><Button onClick={() => setOpen(false)}>Cancel</Button><SubmitButton form="budget-policy" pending={save.isPending} disabled={!form.token_limit && !form.cost_limit}>Save policy</SubmitButton></>}><Form id="budget-policy" className="form-grid" onSubmit={() => save.mutateAsync()}><label className="full-span"><span>Policy name *</span><input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label><label><span>Monthly token limit</span><input type="number" min="1" value={form.token_limit} onChange={(event) => setForm({ ...form, token_limit: event.target.value })} /></label><label><span>Monthly cost limit (USD)</span><input type="number" min="0.01" step="0.01" value={form.cost_limit} onChange={(event) => setForm({ ...form, cost_limit: event.target.value })} /></label><label><span>Warning threshold (%)</span><input type="number" min="1" max="100" value={form.alert_threshold} onChange={(event) => setForm({ ...form, alert_threshold: event.target.value })} /></label><label><span>Enforcement</span><select value={form.enforcement} onChange={(event) => setForm({ ...form, enforcement: event.target.value })}><option value="WARN">Warn only</option><option value="BLOCK">Block at limit</option></select></label>{!form.token_limit && !form.cost_limit && <div className="inline-warning full-span">Set a token limit, a cost limit, or both.</div>}{save.isError && <div className="form-error full-span">{save.error instanceof Error ? save.error.message : 'Unable to save budget policy.'}</div>}</Form></Modal>
  </div>
}

function BudgetMeter({ label, value, limit, format }: { label: string; value: string; limit: string; format: (value: unknown) => string }) {
  const percent = decimalRatioPercent(value, limit)
  const hasLimit = percent !== null
  return <div><span>{label}</span><strong>{format(value)} <small>/ {hasLimit ? format(limit) : 'No limit'}</small></strong><div className="quota-track"><i style={{ width: `${percent ?? 0}%` }} /></div><small>{hasLimit ? `${(percent ?? 0).toFixed(1)}% used` : 'No active limit'}</small></div>
}

function splitTags(value: string) {
  return [...new Set(value.split(',').map((tag) => tag.trim().toLowerCase()).filter(Boolean))]
}

function TagList({ value, empty }: { value: unknown; empty: string }) {
  const tags = Array.isArray(value) ? value.map(String) : []
  return tags.length ? <div className="badge-row">{tags.map((tag) => <Badge key={tag}>{tag}</Badge>)}</div> : <span className="muted-cell">{empty}</span>
}
