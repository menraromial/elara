import pandas as pd
import matplotlib.pyplot as plt
import numpy as np
from io import StringIO
from scipy.interpolate import make_interp_spline

# Données CSV
csv_data = """Heures;Volumes (MWh);Prix (euro/MWh)
00:00-01:00;11117,7;67
01:00-02:00;10987,7;45,01
02:00-03:00;11212,7;37,83
03:00-04:00;12095,3;35,01
04:00-05:00;12623,3;35,01
05:00-06:00;11713,3;39,79
06:00-07:00;12620,8;70
07:00-08:00;11876,8;108,09
08:00-09:00;12166;68,25
09:00-10:00;13958,2;25,58
10:00-11:00;15215,5;17,01
11:00-12:00;14910,7;0
12:00-13:00;17584,4;-0,5
13:00-14:00;17035,8;-2,51
14:00-15:00;16085,2;-2,08
15:00-16:00;15100,9;-0,01
16:00-17:00;13992,3;0,35
17:00-18:00;12692,5;314
18:00-19:00;12063,4;41,32
19:00-20:00;12054,6;93,07
20:00-21:00;12140,2;92,67
21:00-22:00;14399,2;128,07
22:00-23:00;13429;112,3
23:00-00:00;11611;74,55"""

# Lecture des données avec le bon séparateur et conversion des virgules en points
df = pd.read_csv(StringIO(csv_data), sep=';', decimal=',')

# Création des heures pour l'axe X
heures = np.array([int(h.split(':')[0]) for h in df['Heures']])
volumes = df['Volumes (MWh)'].values
prix = df['Prix (euro/MWh)'].values

# Création de courbes lisses
heures_smooth = np.linspace(heures.min(), heures.max(), 300)

# Interpolation spline pour des courbes lisses
spline_volumes = make_interp_spline(heures, volumes, k=3)
volumes_smooth = spline_volumes(heures_smooth)

spline_prix = make_interp_spline(heures, prix, k=3)
prix_smooth = spline_prix(heures_smooth)

# Configuration du graphique
plt.style.use('default')
fig, ax1 = plt.subplots(figsize=(15, 9))
fig.patch.set_facecolor('white')

# Configuration des couleurs
color_volume = '#1f77b4'  # Bleu
color_prix = '#d62728'    # Rouge

# Premier axe Y (Volume)
ax1.set_xlabel('Heures', fontsize=14, fontweight='bold')
ax1.set_ylabel('Volume (MWh)', color=color_volume, fontsize=14, fontweight='bold')

# Courbe de volume lisse
ax1.plot(heures_smooth, volumes_smooth, color=color_volume, linewidth=3, 
         label='Volume (MWh)', alpha=0.8)
# Points originaux
ax1.scatter(heures, volumes, color=color_volume, s=50, zorder=5, alpha=0.7)

ax1.tick_params(axis='y', labelcolor=color_volume, labelsize=12)
ax1.grid(True, alpha=0.3, linestyle='-', linewidth=0.5)

# Deuxième axe Y (Prix)
ax2 = ax1.twinx()
ax2.set_ylabel('Prix (€/MWh)', color=color_prix, fontsize=14, fontweight='bold')

# Courbe de prix lisse
ax2.plot(heures_smooth, prix_smooth, color=color_prix, linewidth=3,
         label='Prix (€/MWh)', alpha=0.8)
# Points originaux
ax2.scatter(heures, prix, color=color_prix, s=50, zorder=5, alpha=0.7)

ax2.tick_params(axis='y', labelcolor=color_prix, labelsize=12)

# Configuration spéciale pour l'axe des prix pour voir les valeurs négatives
prix_min = prix.min()
prix_max = prix.max()
# Forcer l'axe à inclure largement la zone négative
ax2.set_ylim(-50, prix_max + 50)

# Ligne horizontale très visible à y=0
ax2.axhline(y=0, color='black', linestyle='-', linewidth=2, alpha=0.8, zorder=10)
ax2.text(1, 5, 'Prix = 0 €/MWh', fontsize=12, fontweight='bold', 
         bbox=dict(boxstyle="round,pad=0.3", facecolor='yellow', alpha=0.7))

# Configuration de l'axe X
ax1.set_xlim(0, 23)
ax1.set_xticks(range(0, 24, 2))
ax1.set_xticklabels([f'{h:02d}:00' for h in range(0, 24, 2)], fontsize=11)

# Titre principal
plt.title('Évolution du Prix et du Volume d\'Électricité par Heure', 
          fontsize=18, fontweight='bold', pad=25)

# Zone de prix négatifs colorée
ax2.fill_between(heures_smooth, prix_smooth, 0, where=(prix_smooth < 0), 
                 color='red', alpha=0.2, interpolate=True, label='Prix négatifs')

# Zone de prix positifs
ax2.fill_between(heures_smooth, prix_smooth, 0, where=(prix_smooth > 0), 
                 color='green', alpha=0.1, interpolate=True, label='Prix positifs')

# Légende
lines1, labels1 = ax1.get_legend_handles_labels()
lines2, labels2 = ax2.get_legend_handles_labels()
ax1.legend(lines1 + lines2, labels1 + labels2, loc='upper left', 
           frameon=True, shadow=True, fancybox=True, fontsize=12)

# Amélioration des graduations pour les prix négatifs
ax2.yaxis.set_major_locator(plt.MultipleLocator(25))

plt.tight_layout()
plt.savefig('analysis/evolution_prix_volume_electricite.png', dpi=300)
plt.show()

# Affichage de statistiques détaillées sur les prix négatifs
print("=== ANALYSE DES PRIX NÉGATIFS ===")
prix_negatifs = df[df['Prix (euro/MWh)'] < 0]
if not prix_negatifs.empty:
    print(f"Périodes avec prix négatifs : {len(prix_negatifs)} heures")
    for idx, row in prix_negatifs.iterrows():
        print(f"  {row['Heures']} : {row['Prix (euro/MWh)']} €/MWh (Volume: {row['Volumes (MWh)']} MWh)")
else:
    print("Aucun prix négatif détecté")

print(f"\n=== STATISTIQUES GÉNÉRALES ===")
print(f"Prix moyen: {df['Prix (euro/MWh)'].mean():.2f} €/MWh")
print(f"Prix maximum: {df['Prix (euro/MWh)'].max():.2f} €/MWh à {df.loc[df['Prix (euro/MWh)'].idxmax(), 'Heures']}")
print(f"Prix minimum: {df['Prix (euro/MWh)'].min():.2f} €/MWh à {df.loc[df['Prix (euro/MWh)'].idxmin(), 'Heures']}")
print(f"Volume moyen: {df['Volumes (MWh)'].mean():.1f} MWh")
print(f"Amplitude des prix: {df['Prix (euro/MWh)'].max() - df['Prix (euro/MWh)'].min():.2f} €/MWh")