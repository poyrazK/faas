-- Run only in a disposable application database, before enrolling this table.
CREATE TABLE public.products (
 id bigint PRIMARY KEY,
 category text NOT NULL CHECK(category IN ('original','bad-import','human-correction'))
);
INSERT INTO public.products SELECT generate_series(1,500),'original';
