-- Run with the migration owner for a disposable local rehearsal only.
SELECT set_config('request.jwt.claims','{"sub":"alice","tenant_id":"alpha"}',true);
SELECT set_config('gregale.transaction___OP__','alpha',true);
INSERT INTO api.__OP___inventory VALUES('alpha','a_widget',10,500),('alpha','z_widget',1,200);
SELECT set_config('request.jwt.claims','{"sub":"bob","tenant_id":"beta"}',true);
SELECT set_config('gregale.transaction___OP__','beta',true);
INSERT INTO api.__OP___inventory VALUES('beta','a_widget',10,900);
