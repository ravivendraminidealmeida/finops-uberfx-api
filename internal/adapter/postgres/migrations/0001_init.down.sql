DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS inbox;
DROP TRIGGER IF EXISTS wallet_ledger_no_update ON wallet_ledger;
DROP FUNCTION IF EXISTS forbid_ledger_mutation();
DROP TABLE IF EXISTS wallet_ledger;
DROP TABLE IF EXISTS wager_transactions;
DROP TABLE IF EXISTS wallets;
