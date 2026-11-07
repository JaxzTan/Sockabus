-- Row level security on every public table (defence in depth for Supabase's Data API).
-- 000003 already revokes all access from anon/authenticated; with RLS on and no policy for them,
-- they would still see no rows even if a grant came back by mistake. The owner (migrations,
-- SECURITY DEFINER functions such as buru_purge_expired) bypasses RLS. buru_app, and its member
-- login buru_api, get an allow-all policy: grants (000002, append-only REVOKEs) stay the real
-- permission model. No rows are changed.
DO $$
DECLARE
    t text;
BEGIN
    FOR t IN
        SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
    LOOP
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS buru_app_all ON public.%I', t);
        EXECUTE format('CREATE POLICY buru_app_all ON public.%I AS PERMISSIVE FOR ALL TO buru_app USING (true) WITH CHECK (true)', t);
    END LOOP;
END
$$;
