SELECT 'schema_versions', count(*), coalesce(max(version)::text,'') FROM schema_versions
UNION ALL SELECT 'schedules', count(*), coalesce(string_agg(id, ',' ORDER BY id),'') FROM schedules
UNION ALL SELECT 'stop_times', count(*), '' FROM stop_times
UNION ALL SELECT 'captures', count(*), coalesce(md5(string_agg(id||':'||hash, ',' ORDER BY id)),'') FROM captures
UNION ALL SELECT 'fetches', count(*), coalesce(max(id)::text,'') FROM fetches
UNION ALL SELECT 'jobs', count(*), coalesce(md5(string_agg(capture||':'||state||':'||token, ',' ORDER BY capture)),'') FROM jobs
UNION ALL SELECT 'outcomes', count(*), coalesce(md5(string_agg(capture||':'||outcome, ',' ORDER BY capture)),'') FROM outcomes
UNION ALL SELECT 'observations', count(*), coalesce(md5(string_agg(generation||':'||entity||':'||sequence||':'||stop||':'||coalesce(predicted::text,''), ',' ORDER BY generation,entity,sequence)),'') FROM observations
UNION ALL SELECT 'feed_state', count(*), coalesce(string_agg(ns||'/'||feed||'='||generation, ',' ORDER BY ns,feed),'') FROM feed_state;
