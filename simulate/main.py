import math
import time
import random
import logging
import json
import csv
from datetime import datetime
from dataclasses import dataclass
from typing import List, Optional, Dict, Any

# --- CONFIGURATION DES LOGS ---
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S'
)

# --- STRUCTURES DE DONNÉES ---
@dataclass
class Deployment:
    id: str
    replicas_actuels: int
    min_replicas: int
    max_replicas: int
    groupe_id: Optional[str] = None
    poids_ratio: Optional[float] = None

# --- GESTIONNAIRE DE GRAPHIQUE ---
try:
    import matplotlib.pyplot as plt
    MATPLOTLIB_AVAILABLE = True
except ImportError:
    logging.warning("Matplotlib non trouvé. Installez-le avec : pip install matplotlib")
    MATPLOTLIB_AVAILABLE = False

class RealTimePlotter:
    # ... (Code identique)
    def __init__(self, deployment_ids: List[str]):
        if not MATPLOTLIB_AVAILABLE: return
        self.fig, (self.ax1, self.ax2) = plt.subplots(2, 1, figsize=(12, 9), sharex=True)
        plt.ion()
        self.timestamps, self.optimal_power_data, self.current_power_data, self.total_replicas_data = [], [], [], []
        self.individual_replicas_data = {dep_id: [] for dep_id in deployment_ids}
        self.ax1.set_title("Évolution de la Puissance Électrique et du Total des Réplicas")
        self.ax1.set_ylabel("Puissance (kW)", color='tab:blue')
        self.line_optimal_power, = self.ax1.plot([], [], 'k--', label="Puissance Optimale")
        self.line_current_power, = self.ax1.plot([], [], 'b-', label="Puissance Actuelle", lw=2)
        self.ax1b = self.ax1.twinx()
        self.ax1b.set_ylabel("Total Réplicas", color='tab:red')
        self.line_total_replicas, = self.ax1b.plot([], [], 'r-', label="Total Réplicas")
        self.fig.legend(loc="upper center", bbox_to_anchor=(0.5, 1.0), ncol=3)
        self.ax1.grid(True)
        self.ax2.set_title("Évolution des Réplicas par Déploiement")
        self.ax2.set_xlabel("Cycle de Simulation")
        self.ax2.set_ylabel("Nombre de Réplicas")
        self.individual_lines = {dep_id: self.ax2.plot([], [], label=dep_id)[0] for dep_id in deployment_ids}
        self.ax2.legend(loc="upper left", bbox_to_anchor=(1.05, 1.0))
        self.fig.tight_layout(rect=[0, 0, 0.85, 0.95])
    def update(self, cycle, optimal_power, current_power, total_replicas, individual_replicas):
        if not MATPLOTLIB_AVAILABLE: return
        self.timestamps.append(cycle); self.optimal_power_data.append(optimal_power); self.current_power_data.append(current_power); self.total_replicas_data.append(total_replicas)
        self.line_optimal_power.set_data(self.timestamps, self.optimal_power_data); self.line_current_power.set_data(self.timestamps, self.current_power_data); self.line_total_replicas.set_data(self.timestamps, self.total_replicas_data)
        for dep_id, replicas in individual_replicas.items():
            self.individual_replicas_data[dep_id].append(replicas); self.individual_lines[dep_id].set_data(self.timestamps, self.individual_replicas_data[dep_id])
        for ax in [self.ax1, self.ax1b, self.ax2]: ax.relim(), ax.autoscale_view()
        self.fig.canvas.draw(), self.fig.canvas.flush_events(); plt.pause(0.01)

# --- SOURCE D'ÉNERGIE PAR PALIERS (mise à jour) ---
class PowerSource:
    def __init__(self, optimal_power_kw: float, min_plateau_duration: int = 3, max_plateau_duration: int = 8):
        self.optimal_power = optimal_power_kw
        self.min_duration = min_plateau_duration
        self.max_duration = max_plateau_duration
        self.current_power = self.optimal_power
        self.cycles_at_current_level = 0
        self.duration_of_current_plateau = random.randint(self.min_duration, self.max_duration)
        logging.info(f"Source d'énergie initialisée. Palier 1: {self.current_power:.2f} kW pour {self.duration_of_current_plateau} cycles.")

    def update_and_get_power(self) -> float:
        self.cycles_at_current_level += 1
        if self.cycles_at_current_level >= self.duration_of_current_plateau:
            self.cycles_at_current_level = 0
            self.duration_of_current_plateau = random.randint(self.min_duration, self.max_duration)
            # La puissance ne peut plus dépasser l'optimale. Elle varie entre 40% et 100%.
            new_power_factor = random.uniform(0.4, 1.0) 
            self.current_power = self.optimal_power * new_power_factor
            logging.info(f"Source d'énergie: Changement de palier. Nouvelle puissance: {self.current_power:.2f} kW pour les {self.duration_of_current_plateau} prochains cycles.")
        return self.current_power

    def get_reduction_percentage(self) -> float:
        return (self.optimal_power - self.current_power) / self.optimal_power

# --- SCALER (version déclarative) ---
class EnergyAwareScaler:
    def __init__(self, deployments: List[Deployment]):
        self.deployments = deployments
        self.deployment_map = {d.id: d for d in deployments}

    def _distribute_integers_proportionally(self, total_to_distribute, entities):
        # ... (Logique inchangée)
        total_weight = sum(entities.values())
        if total_weight == 0: return {name: 0 for name in entities}
        brut_values = {name: (weight / total_weight) * total_to_distribute for name, weight in entities.items()}
        floor_values = {name: math.floor(val) for name, val in brut_values.items()}
        remainder_to_distribute = total_to_distribute - sum(floor_values.values())
        fractional_parts = {name: val - floor_values[name] for name, val in brut_values.items()}
        sorted_entities = sorted(fractional_parts.items(), key=lambda item: item[1], reverse=True)
        final_distribution = floor_values.copy()
        for i in range(int(remainder_to_distribute)): final_distribution[sorted_entities[i][0]] += 1
        return final_distribution

    def calculate_target_state(self, reduction_percentage: float) -> Dict[str, Any]:
        """
        Calcule l'état final du cluster basé sur le pourcentage de baisse actuel.
        Le calcul part toujours de l'état optimal (max_replicas).
        """
        logging.info("--- Calcul de l'état cible du cluster ---")

        # Étape 1: Calculer le nombre total de réplicas à retirer DE L'ÉTAT MAXIMAL
        total_max_replicas = sum(d.max_replicas for d in self.deployments)
        global_reduction_target = math.ceil(total_max_replicas * reduction_percentage)
        logging.info(f"Pourcentage de baisse: {reduction_percentage:.2%}. Objectif de réduction depuis le max: {global_reduction_target} réplicas")

        if global_reduction_target == 0:
            logging.info("Aucune baisse de puissance. L'état cible est l'état optimal (max réplicas).")
            return {'final_state': [{'id': d.id, 'final_replicas': d.max_replicas} for d in self.deployments]}

        # Étape 2: Répartir la réduction entre les entités (basé sur leur max_replicas)
        entity_potentials = {}
        groups = {d.groupe_id for d in self.deployments if d.groupe_id}
        for group_id in groups: entity_potentials[group_id] = sum(d.max_replicas for d in self.deployments if d.groupe_id == group_id)
        for d in self.deployments:
            if d.groupe_id is None: entity_potentials[d.id] = d.max_replicas
        entity_reduction_targets = self._distribute_integers_proportionally(global_reduction_target, entity_potentials)

        # Étape 3: Répartir la réduction par déploiement au sein des groupes
        desired_reductions_from_max = {}
        for entity_id, reduction_target in entity_reduction_targets.items():
            if entity_id in self.deployment_map and self.deployment_map[entity_id].groupe_id is None:
                desired_reductions_from_max[entity_id] = reduction_target
            else:
                group_weights = {d.id: d.poids_ratio for d in self.deployments if d.groupe_id == entity_id}
                group_distribution = self._distribute_integers_proportionally(reduction_target, group_weights)
                desired_reductions_from_max.update(group_distribution)

        # Étape 4: Calculer les cibles, appliquer les contraintes et gérer le déficit
        final_reductions_from_max, total_deficit = {}, 0
        for d in self.deployments:
            max_possible_reduction = d.max_replicas - d.min_replicas
            actual_reduction = min(desired_reductions_from_max.get(d.id, 0), max_possible_reduction)
            final_reductions_from_max[d.id] = actual_reduction
            total_deficit += (desired_reductions_from_max.get(d.id, 0) - actual_reduction)

        while total_deficit > 0:
            donors = [{'d': d, 'm': (d.max_replicas - d.min_replicas) - final_reductions_from_max.get(d.id, 0)} for d in self.deployments]
            donors = [d for d in donors if d['m'] > 0]
            if not donors:
                logging.warning(f"Impossible de reporter le déficit restant de {total_deficit} réplicas.")
                break
            best_donor = max(donors, key=lambda x: x['m'])
            final_reductions_from_max[best_donor['d'].id] += 1
            total_deficit -= 1
        
        logging.info(f"Réductions finales depuis le max: {final_reductions_from_max}")

        # Étape 5: Calculer le nombre de réplicas final
        final_state = []
        for d in self.deployments:
            final_replicas = d.max_replicas - final_reductions_from_max.get(d.id, 0)
            final_state.append({'id': d.id, 'final_replicas': final_replicas})
        
        return {'final_state': final_state}


# --- SAUVEGARDE DE DONNÉES ---
def save_simulation_data(plotter: RealTimePlotter):
   
    if not plotter or not plotter.timestamps: logging.warning("Aucune donnée à enregistrer."); return
    timestamp_str = datetime.now().strftime('%Y%m%d_%H%M%S'); 
    base_filename = f"simulation_results_{timestamp_str}"
    # json_filename = f"{base_filename}.json"; json_data = {"simulation_metadata": {"total_cycles": len(plotter.timestamps), "save_time_utc": datetime.utcnow().isoformat()}, "time_series": {"cycle": plotter.timestamps, "optimal_power_kw": plotter.optimal_power_data, "current_power_kw": plotter.current_power_data, "total_replicas": plotter.total_replicas_data, "individual_replicas": plotter.individual_replicas_data}};
    # try:
    #     with open(json_filename, 'w') as f: json.dump(json_data, f, indent=4)
    #     logging.info(f"Données de simulation enregistrées dans : {json_filename}")
    # except IOError as e: logging.error(f"Erreur lors de l'enregistrement du fichier JSON : {e}")
    csv_filename = f"{base_filename}.csv"
    try:
        header = ['cycle', 'optimal_power_kw', 'current_power_kw', 'total_replicas']; header.extend([f"replicas_{dep_id}" for dep_id in plotter.individual_replicas_data.keys()]); rows = [];
        for i in range(len(plotter.timestamps)):
            row = {'cycle': plotter.timestamps[i], 'optimal_power_kw': plotter.optimal_power_data[i], 'current_power_kw': plotter.current_power_data[i], 'total_replicas': plotter.total_replicas_data[i]};
            for dep_id in plotter.individual_replicas_data.keys(): row[f"replicas_{dep_id}"] = plotter.individual_replicas_data[dep_id][i]
            rows.append(row)
        with open(csv_filename, 'w', newline='', encoding='utf-8') as f: writer = csv.DictWriter(f, fieldnames=header); writer.writeheader(); writer.writerows(rows)
        logging.info(f"Données de simulation enregistrées dans : {csv_filename}")
    except IOError as e: logging.error(f"Erreur lors de l'enregistrement du fichier CSV : {e}")

# --- BOUCLE DE SIMULATION PRINCIPALE (mise à jour) ---
if __name__ == "__main__":
    SIMULATION_INTERVAL_SECONDS = 10
    cluster_topology = [Deployment(id="webapp-server", groupe_id="frontend-group", replicas_actuels=0, min_replicas=2, max_replicas=20, poids_ratio=3.0), 
                        Deployment(id="asset-cdn", groupe_id="frontend-group", replicas_actuels=0, min_replicas=1, max_replicas=10, poids_ratio=1.0), 
                        Deployment(id="api-gateway", groupe_id="backend-group", replicas_actuels=0, min_replicas=4, max_replicas=15, poids_ratio=2.0), 
                        Deployment(id="user-service", groupe_id="backend-group", replicas_actuels=0, min_replicas=3, max_replicas=10, poids_ratio=2.0), 
                        Deployment(id="logging-service", replicas_actuels=0, min_replicas=2, max_replicas=5), 
                        Deployment(id="critical-db", replicas_actuels=0, min_replicas=2, max_replicas=3)]

    logging.info("Initialisation du cluster : tous les déploiements commencent à leur 'max_replicas'.")
    for d in cluster_topology:
        d.replicas_actuels = d.max_replicas
    
    power_source = PowerSource(optimal_power_kw=100.0)
    plotter = RealTimePlotter(deployment_ids=[d.id for d in cluster_topology])
    
    cycle = 0
    try:
        while True:
            cycle += 1
            logging.info(f"===== DÉBUT DU CYCLE N°{cycle} =====")
            
            # 1. Obtenir l'état de la puissance
            current_power = power_source.update_and_get_power()
            reduction_percentage = power_source.get_reduction_percentage()
            logging.info(f"Puissance Actuelle: {current_power:.2f} kW / {power_source.optimal_power:.2f} kW")

            # 2. Calculer l'état cible du cluster basé sur la puissance actuelle
            scaler = EnergyAwareScaler(cluster_topology)
            result = scaler.calculate_target_state(reduction_percentage)
            
            # 3. Mettre à jour l'état du cluster pour correspondre à l'état cible
            if result and result.get('final_state'):
                current_state_map = {d.id: d for d in cluster_topology}
                for final_state in result['final_state']:
                    current_state_map[final_state['id']].replicas_actuels = final_state['final_replicas']

            # 4. Log et affichage
            logging.info("--- État du cluster après synchronisation ---")
            total_replicas = sum(d.replicas_actuels for d in cluster_topology)
            individual_replicas = {d.id: d.replicas_actuels for d in cluster_topology}
            logging.info(f"Total des réplicas actifs : {total_replicas}")
            
            plotter.update(cycle, power_source.optimal_power, current_power, total_replicas, individual_replicas)
            
            logging.info(f"Fin du cycle. Prochain cycle dans {SIMULATION_INTERVAL_SECONDS} secondes.")
            time.sleep(SIMULATION_INTERVAL_SECONDS)
            
    except KeyboardInterrupt:
        logging.info("Simulation interrompue par l'utilisateur.")
    finally:
        logging.info("Fin de la simulation. Sauvegarde des données...")
        save_simulation_data(plotter)
        if MATPLOTLIB_AVAILABLE:
            plt.ioff()
            logging.info("Graphique final. Fermez la fenêtre du graphique pour quitter.")
            plt.show()