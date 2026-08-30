package provisioning

import "github.com/relayedock/relayedock/internal/domain"

func domainDecimal(value string) (domain.Decimal, error) { return domain.ParseDecimal(value) }
