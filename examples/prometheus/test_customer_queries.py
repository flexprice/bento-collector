import unittest

from customer_queries import METRICS, TENANT_LABEL, metric_settings, query_for


class CustomerQueriesTest(unittest.TestCase):
    def test_every_variant_is_tenant_scoped(self):
        for metric in METRICS:
            with self.subTest(metric=metric):
                query = query_for(metric, 'tenant"\\test')
                self.assertIn('"tenant\\"\\\\test"', query)
                self.assertNotIn("increase(", query)
                self.assertIn(metric_settings(metric)["PROMETHEUS_METRIC_MODE"], ("window", "usage_date"))

    def test_reservations_keep_instance_and_minute_conversion(self):
        query = query_for("cpu_hours", "demo", "7d", " @ 1791676800")
        for expected in (TENANT_LABEL, "sum by (instance)", "sum_over_time(", "[7d] @ 1791676800", "/ 60"):
            self.assertIn(expected, query)

    def test_snapshot_is_filtered_to_one_usage_day(self):
        query = query_for("spark_data_written_gb", "demo")
        self.assertIn('usage_date="__USAGE_DATE__"', query)
        self.assertIn("sum by (usage_date)", query)
        self.assertNotIn("[1d]", query)


if __name__ == "__main__":
    unittest.main()
