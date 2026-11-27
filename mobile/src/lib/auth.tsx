import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { api, Me, setToken, setUnauthorizedHandler } from './api';
import { deleteItem, getItem, setItem } from './storage';

const KEY = 'eimsky.token';

type AuthState = {
  ready: boolean;
  signedIn: boolean;
  me: Me | null;
  signIn: (token: string) => Promise<void>;
  signOut: () => Promise<void>;
  refresh: () => Promise<Me | null>;
};

const Ctx = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [ready, setReady] = useState(false);
  const [tok, setTok] = useState<string | null>(null);
  const [me, setMe] = useState<Me | null>(null);

  const clear = useCallback(async () => {
    setToken(null);
    setTok(null);
    setMe(null);
    await deleteItem(KEY);
  }, []);

  const refresh = useCallback(async () => {
    try {
      const m = await api.me();
      setMe(m);
      return m;
    } catch {
      return null;
    }
  }, []);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      void clear();
    });
    (async () => {
      const saved = await getItem(KEY);
      if (saved) {
        setToken(saved);
        setTok(saved);
        await refresh();
      }
      setReady(true);
    })();
    return () => setUnauthorizedHandler(null);
  }, [clear, refresh]);

  const signIn = useCallback(
    async (t: string) => {
      setToken(t);
      await setItem(KEY, t);
      await refresh(); // load the profile before the app decides where to go
      setTok(t);
    },
    [refresh],
  );

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } catch {}
    await clear();
  }, [clear]);

  const value = useMemo(() => ({ ready, signedIn: !!tok, me, signIn, signOut, refresh }), [ready, tok, me, signIn, signOut, refresh]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useAuth(): AuthState {
  const v = useContext(Ctx);
  if (!v) throw new Error('useAuth outside AuthProvider');
  return v;
}

/** Where a signed-in forwarder belongs given its registration state. */
export function homeFor(me: Me | null): '/(tabs)' | '/register' | '/pending' {
  if (!me) return '/register';
  if (me.user.companyStatus === 'approved') return '/(tabs)';
  if (!me.registration.submitted || me.user.companyStatus === 'none' || me.user.companyStatus === 'rejected') return '/register';
  return '/pending';
}
