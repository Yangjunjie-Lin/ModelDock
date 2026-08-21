import { useState, type ReactNode } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { ArrowRight, KeyRound, MailCheck, Network, ShieldCheck, UserPlus } from 'lucide-react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Form, SubmitButton } from '../components/ui'
import { api } from '../lib/api'

type RegistrationConfig = { registration_mode: 'CLOSED' | 'INVITE_ONLY' | 'PUBLIC' }
type Invitation = { organization_name: string; email: string; role: string; expires_at: string }

function AuthLayout({ eyebrow, title, description, children, footer }: { eyebrow: string; title: string; description: string; children: ReactNode; footer?: ReactNode }) {
  return <main className="login-page console-login"><section className="login-story"><div className="login-brand"><span className="brand-mark"><Network size={20} /></span><strong>ModelDock</strong></div><div className="login-message"><div className="story-badge"><span />SECURE ACCOUNT ACCESS</div><h1>Your workspace starts with a verified identity.</h1><p>Account recovery, organization invitations, and session controls use expiring one-time links.</p><div className="login-features"><span><MailCheck size={17} />Verified email ownership</span><span><KeyRound size={17} />Protected account recovery</span><span><ShieldCheck size={17} />Audited security events</span></div></div><p className="login-foot">ModelDock Console</p></section><section className="login-form-side"><div className="login-card"><div><span className="eyebrow">{eyebrow}</span><h2>{title}</h2><p>{description}</p></div>{children}{footer && <p className="login-help">{footer}</p>}</div></section></main>
}

export function RegisterPage() {
  const [params] = useSearchParams()
  const [form, setForm] = useState({ email: '', display_name: '', password: '', registration_code: params.get('code') || '', invite_token: params.get('invite_token') || '' })
  const config = useQuery({ queryKey: ['registration-config'], queryFn: () => api<RegistrationConfig>('/auth/config') })
  const register = useMutation({ mutationFn: () => api<{ email_verification_required?: boolean }>('/auth/register', { method: 'POST', body: JSON.stringify(form) }) })
  const closed = config.data?.registration_mode === 'CLOSED'
  if (register.isSuccess) return <AuthLayout eyebrow="CHECK YOUR EMAIL" title="Registration received" description="Open the verification message to activate your account." footer={<Link className="text-link" to="/login">Return to sign in</Link>}><div className="inline-note">The response is intentionally identical for existing and new addresses.</div></AuthLayout>
  return <AuthLayout eyebrow="CREATE ACCOUNT" title="Start a workspace" description={closed ? 'Public registration is currently closed.' : 'Use your work email and a password of at least 12 characters.'} footer={<><Link className="text-link" to="/login">Already have an account? Sign in</Link></>}><Form className="form-stack" onSubmit={() => register.mutateAsync()}><label><span>Work email</span><input required type="email" autoComplete="email" value={form.email} onChange={(event) => setForm({ ...form, email: event.target.value })} /></label><label><span>Display name</span><input required autoComplete="name" value={form.display_name} onChange={(event) => setForm({ ...form, display_name: event.target.value })} /></label><label><span>Password</span><input required minLength={12} type="password" autoComplete="new-password" value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} /></label>{config.data?.registration_mode === 'INVITE_ONLY' && !form.invite_token && <label><span>Registration code</span><input required value={form.registration_code} onChange={(event) => setForm({ ...form, registration_code: event.target.value })} /></label>}{register.isError && <div className="form-error">{register.error instanceof Error ? register.error.message : 'Registration failed.'}</div>}<SubmitButton pending={register.isPending} disabled={closed || config.isLoading}>Create account<ArrowRight size={15} /></SubmitButton></Form></AuthLayout>
}

export function ForgotPasswordPage() {
  const [email, setEmail] = useState('')
  const request = useMutation({ mutationFn: () => api('/auth/forgot-password', { method: 'POST', body: JSON.stringify({ email }) }) })
  return <AuthLayout eyebrow="ACCOUNT RECOVERY" title="Reset your password" description="Enter your account email to request an expiring reset link." footer={<Link className="text-link" to="/login">Return to sign in</Link>}>{request.isSuccess ? <div className="inline-note">If the account exists, a password reset email has been queued.</div> : <Form className="form-stack" onSubmit={() => request.mutateAsync()}><label><span>Email address</span><input required type="email" autoComplete="email" value={email} onChange={(event) => setEmail(event.target.value)} /></label>{request.isError && <div className="form-error">{request.error instanceof Error ? request.error.message : 'Request failed.'}</div>}<SubmitButton pending={request.isPending}>Send reset link<ArrowRight size={15} /></SubmitButton></Form>}</AuthLayout>
}

export function ResetPasswordPage() {
  const [params] = useSearchParams()
  const [password, setPassword] = useState('')
  const token = params.get('token') || ''
  const reset = useMutation({ mutationFn: () => api('/auth/reset-password', { method: 'POST', body: JSON.stringify({ token, password }) }) })
  return <AuthLayout eyebrow="NEW PASSWORD" title="Choose a new password" description="This link expires and can be used only once." footer={<Link className="text-link" to="/login">Return to sign in</Link>}>{reset.isSuccess ? <div className="inline-note">Your password was reset. Existing refresh sessions have been revoked.</div> : <Form className="form-stack" onSubmit={() => reset.mutateAsync()}><label><span>New password</span><input required minLength={12} type="password" autoComplete="new-password" value={password} onChange={(event) => setPassword(event.target.value)} /></label>{reset.isError && <div className="form-error">{reset.error instanceof Error ? reset.error.message : 'Reset failed.'}</div>}<SubmitButton pending={reset.isPending} disabled={!token}>Reset password<ArrowRight size={15} /></SubmitButton></Form>}</AuthLayout>
}

export function VerifyEmailPage() {
  const [params] = useSearchParams()
  const token = params.get('token') || ''
  const verify = useMutation({ mutationFn: () => api('/auth/verify-email', { method: 'POST', body: JSON.stringify({ token }) }) })
  return <AuthLayout eyebrow="EMAIL VERIFICATION" title="Activate your account" description="Confirm this address before signing in to your workspace." footer={<Link className="text-link" to="/login">Return to sign in</Link>}>{verify.isSuccess ? <div className="inline-note">Email verified. You can now sign in and create an API key.</div> : <><SubmitButton pending={verify.isPending} disabled={!token} onClick={() => verify.mutate()}>Verify email<MailCheck size={15} /></SubmitButton>{verify.isError && <div className="form-error">{verify.error instanceof Error ? verify.error.message : 'Verification failed.'}</div>}</>}</AuthLayout>
}

export function InvitationPage() {
  const { token = '' } = useParams()
  const navigate = useNavigate()
  const [form, setForm] = useState({ display_name: '', password: '' })
  const invitation = useQuery({ queryKey: ['public-invitation', token], queryFn: () => api<Invitation>(`/auth/invitations/${token}`), enabled: Boolean(token), retry: false })
  const accept = useMutation({ mutationFn: () => api(`/auth/invitations/${token}/accept`, { method: 'POST', body: JSON.stringify(form) }), onSuccess: () => navigate('/login?invitation=accepted') })
  const reject = useMutation({ mutationFn: () => api(`/auth/invitations/${token}/reject`, { method: 'POST' }), onSuccess: () => navigate('/login?invitation=rejected') })
  return <AuthLayout eyebrow="ORGANIZATION INVITATION" title={invitation.data ? `Join ${invitation.data.organization_name}` : 'Review invitation'} description={invitation.data ? `${invitation.data.email} was invited as ${invitation.data.role.toLowerCase()}.` : 'Loading the invitation details.'} footer={<Link className="text-link" to="/login">Return to sign in</Link>}>{invitation.isError && <div className="form-error">{invitation.error instanceof Error ? invitation.error.message : 'Invitation unavailable.'}</div>}{invitation.isSuccess && <Form className="form-stack" onSubmit={() => accept.mutateAsync()}><label><span>Display name for a new account</span><input value={form.display_name} onChange={(event) => setForm({ ...form, display_name: event.target.value })} /></label><label><span>Password for a new account</span><input minLength={12} type="password" autoComplete="new-password" value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} /></label>{accept.isError && <div className="form-error">{accept.error instanceof Error ? accept.error.message : 'Could not accept invitation.'}</div>}<SubmitButton pending={accept.isPending}>Accept invitation<UserPlus size={15} /></SubmitButton><SubmitButton type="button" pending={reject.isPending} onClick={() => reject.mutate()}>Decline invitation</SubmitButton></Form>}</AuthLayout>
}
