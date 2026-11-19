# Financial market series

This context covers Brazilian financial market indexes and the macroeconomic series used alongside them as reference data.

## Series language

**Series**:
A named, dated sequence of values with one unit, source, and frequency. It is the shared storage and charting concept for both market indexes and economic indicators.
_Avoid_: Metric, tape

**Market index**:
A published measure of the performance of a defined market, asset class, or portfolio, such as Ibovespa, IFIX, or IMA-B.
_Avoid_: Benchmark (unless it is explicitly being used to compare another result)

**Economic indicator**:
A published macroeconomic or market reference series, such as IPCA, Selic, PTAX, or Focus expectations.
_Avoid_: Index (when the source does not define it as one)

**Observation**:
One value for a series on a reference date and optional dimension. A correction creates a new revision while preserving the previous value.
_Avoid_: Quote (unless the value is a traded instrument price)

**Final value**:
The latest published observation for a series, optionally scoped to one dimension such as a Focus target year or NTN-B maturity year.
_Avoid_: Closing price (for non-price series)
