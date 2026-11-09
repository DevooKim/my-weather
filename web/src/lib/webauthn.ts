// 패스키 등록·로그인 (§2.8). 서버는 go-webauthn 형식(base64url JSON)을 주고받는다.
import { api } from './api';
import { deviceLabel } from './push';

const b64 = {
	dec(s: string): ArrayBuffer {
		const pad = '='.repeat((4 - (s.length % 4)) % 4);
		const raw = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'));
		const out = new Uint8Array(raw.length);
		for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
		return out.buffer;
	},
	enc(b: ArrayBuffer | null): string | null {
		if (!b) return null;
		const u = new Uint8Array(b);
		let s = '';
		for (let i = 0; i < u.length; i++) s += String.fromCharCode(u[i]);
		return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
	}
};

type PKC = typeof PublicKeyCredential & {
	parseCreationOptionsFromJSON?: (o: any) => PublicKeyCredentialCreationOptions;
	parseRequestOptionsFromJSON?: (o: any) => PublicKeyCredentialRequestOptions;
};

export function passkeySupported() {
	return typeof window !== 'undefined' && 'PublicKeyCredential' in window && !!navigator.credentials;
}

function creationOptions(o: any): PublicKeyCredentialCreationOptions {
	const P = PublicKeyCredential as PKC;
	if (P.parseCreationOptionsFromJSON) return P.parseCreationOptionsFromJSON(o);
	return {
		...o,
		challenge: b64.dec(o.challenge),
		user: { ...o.user, id: b64.dec(o.user.id) },
		excludeCredentials: (o.excludeCredentials ?? []).map((c: any) => ({ ...c, id: b64.dec(c.id) }))
	};
}

function requestOptions(o: any): PublicKeyCredentialRequestOptions {
	const P = PublicKeyCredential as PKC;
	if (P.parseRequestOptionsFromJSON) return P.parseRequestOptionsFromJSON(o);
	return {
		...o,
		challenge: b64.dec(o.challenge),
		allowCredentials: (o.allowCredentials ?? []).map((c: any) => ({ ...c, id: b64.dec(c.id) }))
	};
}

function credJSON(c: PublicKeyCredential): any {
	const anyC = c as any;
	if (typeof anyC.toJSON === 'function') {
		try {
			return anyC.toJSON();
		} catch {
			/* 일부 Safari는 toJSON이 있어도 실패 */
		}
	}
	const r = c.response as any;
	const response: any = { clientDataJSON: b64.enc(r.clientDataJSON) };
	if (r.attestationObject) {
		response.attestationObject = b64.enc(r.attestationObject);
		response.transports = typeof r.getTransports === 'function' ? r.getTransports() : undefined;
	} else {
		response.authenticatorData = b64.enc(r.authenticatorData);
		response.signature = b64.enc(r.signature);
		response.userHandle = b64.enc(r.userHandle);
	}
	return {
		id: c.id,
		rawId: b64.enc(c.rawId),
		type: c.type,
		authenticatorAttachment: (c as any).authenticatorAttachment ?? undefined,
		clientExtensionResults: c.getClientExtensionResults?.() ?? {},
		response
	};
}

function friendly(e: unknown): Error {
	const name = (e as any)?.name;
	if (name === 'NotAllowedError' || name === 'AbortError') return new Error('취소했거나 시간이 지났어요.');
	if (name === 'InvalidStateError') return new Error('이 기기는 이미 등록돼 있어요. 패스키로 로그인하세요.');
	if (name === 'SecurityError') return new Error('이 주소에서는 패스키를 쓸 수 없어요.');
	return e instanceof Error ? e : new Error('패스키를 쓰지 못했어요.');
}

export async function loginWithPasskey() {
	const { publicKey } = await api.loginOptions();
	let cred: Credential | null;
	try {
		cred = await navigator.credentials.get({ publicKey: requestOptions(publicKey) });
	} catch (e) {
		throw friendly(e);
	}
	if (!cred) throw new Error('패스키를 고르지 않았어요.');
	return api.loginVerify(credJSON(cred as PublicKeyCredential));
}

/** 코드 형식 MW-XXXX-XXXX-XXXX. 하이픈·대소문자는 서버가 무시하지만 보기 좋게 정리한다. */
export function normalizeToken(s: string) {
	return s.trim().toUpperCase().replace(/\s+/g, '');
}

export async function registerPasskey(token: string) {
	const t = normalizeToken(token);
	const { publicKey } = await api.registerOptions(t, deviceLabel());
	let cred: Credential | null;
	try {
		cred = await navigator.credentials.create({ publicKey: creationOptions(publicKey) });
	} catch (e) {
		throw friendly(e);
	}
	if (!cred) throw new Error('패스키를 만들지 않았어요.');
	return api.registerVerify(t, credJSON(cred as PublicKeyCredential));
}
